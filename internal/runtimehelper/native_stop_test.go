package runtimehelper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func nativeStopCommands(t *testing.T, before, after string, stopFails bool) (string, string) {
	t.Helper()
	directory := t.TempDir()
	state, stopped := filepath.Join(directory, "state"), filepath.Join(directory, "stopped")
	if err := os.WriteFile(state, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stopped, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNIT_STATE", state)
	t.Setenv("STOPPED_STATE", stopped)
	fail := "0"
	if stopFails {
		fail = "1"
	}
	t.Setenv("FAIL_STOP", fail)
	command := writeTestCommand(t, "systemctl", `
case "$1" in
  show) cat "$UNIT_STATE" ;;
  stop) if [ "$FAIL_STOP" = 1 ]; then exit 1; fi; cp "$STOPPED_STATE" "$UNIT_STATE" ;;
  *) exit 1 ;;
esac
`)
	return command, directory
}

func unitFields(active, group, result, code, status string) string {
	subState := "dead"
	if active == "active" {
		subState = "running"
	}
	return "LoadState=loaded\nActiveState=" + active + "\nSubState=" + subState + "\nControlGroup=" + group + "\nResult=" + result + "\nExecMainCode=" + code + "\nExecMainStatus=" + status + "\n"
}

func TestNativeStopRequiresEmptyProcessGroupAndSeparatesUncleanExit(t *testing.T) {
	unit := "agent-remote-session-test.service"
	group := "/system.slice/" + unit
	for _, test := range []struct {
		name, population, active, result, code, status string
		failStop, wantError, unclean                   bool
	}{
		{"clean", "0", "inactive", "success", "1", "0", false, false, false},
		{"killed", "0", "failed", "signal", "2", "9", false, false, true},
		{"writers", "1", "inactive", "success", "1", "0", false, true, false},
		{"stop_failed", "0", "inactive", "success", "1", "0", true, true, false},
		{"still_running", "0", "active", "success", "1", "0", false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, _ := nativeStopCommands(t, unitFields("active", group, "success", "0", "0"), unitFields(test.active, "", test.result, test.code, test.status), test.failStop)
			root := t.TempDir()
			groupPath := filepath.Join(root, strings.TrimPrefix(group, "/"))
			if err := os.MkdirAll(groupPath, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(groupPath, "cgroup.events"), []byte("populated "+test.population+"\nfrozen 0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			engine := NewEngine(EngineConfig{SystemctlPath: command, CgroupRoot: root})
			result, err := engine.stopNativeWriters(context.Background(), SessionSpec{UnitName: unit, BootID: currentBootID()})
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected stop result: %v", err)
			}
			if err == nil && result.Unclean != (test.unclean || currentBootID() == "") {
				t.Fatalf("wrong exit classification: %#v", result)
			}
		})
	}
}

func TestNativeStopManagedRequiresNormalExitBeforeForcedCleanup(t *testing.T) {
	if currentBootID() == "" {
		t.Skip("requires a kernel boot identity")
	}
	for _, graceful := range []bool{false, true} {
		t.Run(map[bool]string{false: "forced-success", true: "graceful-success"}[graceful], func(t *testing.T) {
			unit := "session.service"
			group := "/system.slice/" + unit
			command, directory := nativeStopCommands(t, unitFields("active", group, "success", "0", "0"), unitFields("inactive", "", "success", "1", "0"), false)
			root := t.TempDir()
			groupPath := filepath.Join(root, strings.TrimPrefix(group, "/"))
			if err := os.MkdirAll(groupPath, 0o700); err != nil {
				t.Fatal(err)
			}
			events := filepath.Join(groupPath, "cgroup.events")
			if err := os.WriteFile(events, []byte("populated 1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			exited := filepath.Join(directory, "exited")
			state := strings.ReplaceAll(unitFields("active", group, "success", "1", "0"), "SubState=running", "SubState=exited")
			if err := os.WriteFile(exited, []byte(state), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("EXITED_STATE", exited)
			t.Setenv("CGROUP_EVENTS", events)
			t.Setenv("GRACEFUL_EXIT", map[bool]string{false: "0", true: "1"}[graceful])
			wrapped := writeTestCommand(t, "systemctl-managed-stop", `
if [ "$1" = kill ] && [ "$GRACEFUL_EXIT" = 1 ]; then
  cp "$EXITED_STATE" "$UNIT_STATE"
  printf 'populated 0\n' > "$CGROUP_EVENTS"
  exit 0
fi
if [ "$1" = stop ]; then printf 'populated 0\n' > "$CGROUP_EVENTS"; fi
exec `+shellQuote(command)+` "$@"
`)
			engine := NewEngine(EngineConfig{SystemctlPath: wrapped, CgroupRoot: root})
			spec := SessionSpec{UnitName: unit, BootID: currentBootID(), SkillSnapshotID: "55555555-5555-4555-8555-555555555555"}
			termination, err := engine.stopNativeWritersWithInvocation(context.Background(), spec, "")
			if err != nil || termination.Unclean == graceful {
				t.Fatalf("managed clean classification requires normal exit before forced cleanup: %#v, %v", termination, err)
			}
		})
	}
}

func TestNativeCgroupInspectionFailsClosedOnIncompleteEvidence(t *testing.T) {
	root := t.TempDir()
	group := "/system.slice/session.service"
	directory := filepath.Join(root, strings.TrimPrefix(group, "/"))
	if err := confirmEmptyCgroup(root, group); err != nil {
		t.Fatal("removed cgroup should be empty", err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := confirmEmptyCgroup(root, group); err == nil {
		t.Fatal("missing events file incorrectly proved an existing cgroup empty")
	}
	for _, contents := range []string{"frozen 0\n", "populated 0\npopulated 0\n", "populated 1\n", strings.Repeat("x", 4097)} {
		if err := os.WriteFile(filepath.Join(directory, "cgroup.events"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := confirmEmptyCgroup(root, group); err == nil {
			t.Fatal("incomplete or ambiguous population accepted")
		}
	}
}

func TestNativeStopPreservesSessionFilesWhenStopFails(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("spec validation requires root ownership on Linux")
	}
	state := t.TempDir()
	sessionID, userID := "test-session", "test-user"
	unit := "agent-remote-session-" + shortDigest(sessionID, 12) + ".service"
	command, _ := nativeStopCommands(t, unitFields("active", "/system.slice/"+unit, "success", "0", "0"), "", true)
	engine := NewEngine(EngineConfig{StateRoot: state, WorkspaceRoot: filepath.Join(state, "users"), AccountRoot: filepath.Join(state, "accounts"), SystemctlPath: command, CgroupRoot: t.TempDir()})
	root := filepath.Join(state, "sessions", sessionID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := SessionSpec{
		Version: ProtocolVersion, Kind: "session", SessionID: sessionID, UserID: userID,
		Username: "ar-u-" + shortDigest(userID, 12), WorkspacePath: filepath.Join(engine.config.WorkspaceRoot, userID), AccountPath: filepath.Join(engine.config.AccountRoot, userID),
		SessionRoot: root, RuntimeRoot: filepath.Dir(filepath.Dir(engine.config.ClaudeRuntimePath)), RuntimeCommand: "/opt/agent-remote/runtime/bin/claude",
		TmuxSessionName: "session-test", TmuxSocketPath: filepath.Join(root, "tmux", "tmux.sock"), UnitName: unit,
		NetworkNamespace: "ar-" + shortDigest(sessionID, 10), RuntimeConfig: sessionRuntimeConfigFromEngine(engine.config),
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "only-copy")
	if err := os.WriteFile(marker, []byte("retain runtime state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": sessionID}); err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("expected stop failure, got %v", err)
	}
	if bytes, err := os.ReadFile(marker); err != nil || string(bytes) != "retain runtime state" {
		t.Fatal("failed stop destroyed session files", err)
	}
}

func TestNativeUnitInspectionRejectsAmbiguousEvidence(t *testing.T) {
	valid := unitFields("inactive", "", "success", "1", "0")
	for name, contents := range map[string]string{
		"duplicate":      valid + "ActiveState=inactive\n",
		"missing":        strings.ReplaceAll(valid, "ExecMainCode=1\n", ""),
		"unknown_active": strings.ReplaceAll(valid, "ActiveState=inactive", "ActiveState=unknown"),
		"unknown_load":   strings.ReplaceAll(valid, "LoadState=loaded", "LoadState=error"),
		"unknown_field":  valid + "Untrusted=yes\n",
		"malformed":      valid + "bad line\n",
		"overflow":       valid + strings.Repeat("x", 16*1024),
		"contradictory":  "LoadState=not-found\nActiveState=active\n",
	} {
		t.Run(name, func(t *testing.T) {
			command, _ := nativeStopCommands(t, contents, "", false)
			if _, err := readNativeUnit(context.Background(), command, "session.service"); err == nil {
				t.Fatal("ambiguous unit state accepted")
			}
		})
	}
}

func TestNativeAbsentUnitAfterRebootIsQuiescentButUnclean(t *testing.T) {
	command, _ := nativeStopCommands(t, "LoadState=not-found\nActiveState=inactive\n", "", true)
	engine := NewEngine(EngineConfig{SystemctlPath: command, CgroupRoot: t.TempDir()})
	result, err := engine.stopNativeWriters(context.Background(), SessionSpec{UnitName: "session.service", BootID: "earlier-boot"})
	if err != nil || !result.Unclean {
		t.Fatalf("absent unit recovery must remain unclean: %#v, %v", result, err)
	}
}

func TestNativeFailedLaunchCleanupPreservesResourcesUntilWritersExit(t *testing.T) {
	for _, stopFails := range []bool{true, false} {
		t.Run(map[bool]string{true: "stop_failed", false: "stopped"}[stopFails], func(t *testing.T) {
			command, directory := nativeStopCommands(t, unitFields("active", "/system.slice/session.service", "success", "0", "0"), unitFields("inactive", "", "signal", "2", "15"), stopFails)
			marker := filepath.Join(directory, "cleanup-called")
			t.Setenv("CLEANUP_MARKER", marker)
			cleanup := writeTestCommand(t, "cleanup", `printf '%s\n' "$*" >> "$CLEANUP_MARKER"`)
			sessionRoot := t.TempDir()
			if err := os.Mkdir(filepath.Join(sessionRoot, "tmp"), 0o700); err != nil {
				t.Fatal(err)
			}
			engine := NewEngine(EngineConfig{SystemctlPath: command, CgroupRoot: t.TempDir(), IPPath: cleanup, UmountPath: cleanup})
			err := engine.cleanupFailedNativeLaunch(SessionSpec{UnitName: "session.service", SessionRoot: sessionRoot, NetworkNamespace: "ar-test"})
			if (err != nil) != stopFails {
				t.Fatalf("unexpected cleanup result: %v", err)
			}
			data, readErr := os.ReadFile(marker)
			if stopFails && !os.IsNotExist(readErr) {
				t.Fatal("uncertain writer exit allowed resource cleanup")
			}
			if !stopFails && (readErr != nil || !strings.Contains(string(data), "netns delete ar-test") || !strings.Contains(string(data), filepath.Join(sessionRoot, "tmp"))) {
				t.Fatalf("quiescent cleanup omitted resources: %s, %v", data, readErr)
			}
		})
	}
}

func TestNativeCancelledInspectionCannotAuthorizeCleanup(t *testing.T) {
	command, _ := nativeStopCommands(t, unitFields("inactive", "", "success", "1", "0"), "", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := NewEngine(EngineConfig{SystemctlPath: command, CgroupRoot: t.TempDir()})
	if _, err := engine.stopNativeWriters(ctx, SessionSpec{UnitName: "session.service"}); err == nil {
		t.Fatal("cancelled inspection authorized cleanup")
	}
}
