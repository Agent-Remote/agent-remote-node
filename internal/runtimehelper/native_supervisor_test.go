package runtimehelper

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativePaneExitRequiresObservedNormalSuccess(t *testing.T) {
	for _, test := range []struct {
		value           string
		dead, wantError bool
	}{
		{"0|", false, false}, {"0|0", false, false}, {"1|0", true, false},
		{"1|1", true, true}, {"1|137", true, true}, {"1|", true, true},
		{"1|+0", true, true}, {"1|00", true, true}, {"1|-1", true, true},
		{"1|256", true, true}, {"1|0|other", false, true}, {"1|0\nother", false, true},
		{"", false, true}, {"unknown|0", false, true},
	} {
		t.Run(test.value, func(t *testing.T) {
			dead, err := parseNativePaneExit(test.value)
			if dead != test.dead || (err != nil) != test.wantError {
				t.Fatalf("unexpected pane classification: dead=%v err=%v", dead, err)
			}
		})
	}
}

func TestNativeNaturalExitSurvivesUnitCollection(t *testing.T) {
	unit := "session.service"
	before := strings.ReplaceAll(unitFields("active", "", "success", "1", "0"), "SubState=running", "SubState=exited")
	command, _ := nativeStopCommands(t, before, "LoadState=not-found\nActiveState=inactive\n", false)
	engine := NewEngine(EngineConfig{SystemctlPath: command, CgroupRoot: t.TempDir()})
	spec := SessionSpec{UnitName: unit, BootID: currentBootID(), SkillSnapshotID: "55555555-5555-4555-8555-555555555555"}
	if active, err := engine.nativeSessionActive(spec); err != nil || active {
		t.Fatalf("exited retained unit was still reported running: active=%v err=%v", active, err)
	}
	termination, err := engine.stopNativeWritersWithInvocation(context.Background(), spec, "")
	if currentBootID() == "" {
		if err == nil {
			t.Fatal("managed stop accepted an unavailable kernel boot identity")
		}
		return
	}
	if err != nil || termination.Unclean != (currentBootID() == "") {
		t.Fatalf("unit collection destroyed normal exit evidence: %#v, %v", termination, err)
	}
}

func TestNativeExitedSupervisorWithRemainingWritersIsNotClean(t *testing.T) {
	unit := "session.service"
	before := strings.ReplaceAll(unitFields("active", "/system.slice/"+unit, "success", "1", "0"), "SubState=running", "SubState=exited")
	command, _ := nativeStopCommands(t, before, unitFields("inactive", "", "success", "1", "0"), false)
	root := t.TempDir()
	group := filepath.Join(root, "system.slice", unit)
	if err := os.MkdirAll(group, 0o700); err != nil {
		t.Fatal(err)
	}
	events := filepath.Join(group, "cgroup.events")
	if err := os.WriteFile(events, []byte("populated 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CGROUP_EVENTS", events)
	wrapped := writeTestCommand(t, "systemctl-wrapped", "if [ \"$1\" = stop ]; then printf 'populated 0\\n' > \"$CGROUP_EVENTS\"; fi\nexec "+shellQuote(command)+" \"$@\"")
	engine := NewEngine(EngineConfig{SystemctlPath: wrapped, CgroupRoot: root})
	spec := SessionSpec{UnitName: unit, BootID: currentBootID(), SkillSnapshotID: "55555555-5555-4555-8555-555555555555"}
	if _, err := engine.nativeSessionActive(spec); err == nil {
		t.Fatal("reconciliation treated remaining writers as stopped")
	}
	termination, err := engine.stopNativeWritersWithInvocation(context.Background(), spec, "")
	if currentBootID() == "" {
		if err == nil {
			t.Fatal("managed stop accepted an unavailable kernel boot identity")
		}
		return
	}
	if err != nil || !termination.Unclean {
		t.Fatalf("stopping residual writers incorrectly published clean state: %#v, %v", termination, err)
	}
}

func TestNativeManagedPaneIntegration(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_MOUNT_TEST") != "1" {
		t.Skip("requires isolated Linux tmux runtime")
	}
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, command         string
		wantError, killServer bool
	}{
		{"normal", "/bin/sh -c 'exit 0'", false, false},
		{"closed_terminal_before_exit", "/bin/sh -c 'trap \"\" HUP; exec </dev/null >/dev/null 2>&1; sleep 0.4; exit 0'", false, false},
		{"error", "/bin/sh -c 'exit 7'", true, false},
		{"sigkill", "/bin/sh -c 'kill -KILL $$'", true, false},
		{"lost_server", "sleep 30", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "ar-pane-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			config := EngineConfig{TmuxBinaryPath: binary}
			spec := SessionSpec{SessionRoot: root, WorkspacePath: root, TmuxSocketPath: filepath.Join(root, "tmux.sock"), TmuxSessionName: "proof", BootID: "current-boot"}
			t.Cleanup(func() { _ = exec.Command(binary, "-S", spec.TmuxSocketPath, "kill-server").Run() })
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- superviseManagedNative(ctx, config, spec, test.command) }()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				value, err := nativeTmuxCommand(ctx, config, spec, "show-window-option", "-v", "-t", spec.TmuxSessionName, "remain-on-exit")
				if err == nil && value == "on" {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("supervisor failed before retention: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			if test.killServer {
				_, _ = nativeTmuxCommand(ctx, config, spec, "kill-server")
			} else {
				digest := sha256.Sum256([]byte(spec.TmuxSessionName))
				if _, err := nativeTmuxCommand(ctx, config, spec, "wait-for", "-S", fmt.Sprintf("agent-remote-client-%x", digest[:8])); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if (err != nil) != test.wantError {
					t.Fatalf("wrong real pane outcome: %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
