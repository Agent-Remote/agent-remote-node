package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func nativeSkillFixture(t *testing.T, unclean bool) (Engine, SessionSpec, skillmanager.SnapshotBinding) {
	t.Helper()
	return nativeSkillFixtureForTask(t, unclean, "")
}

func nativeSkillFixtureForTask(t *testing.T, unclean bool, taskID string) (Engine, SessionSpec, skillmanager.SnapshotBinding) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned Linux skill store")
	}
	root := t.TempDir()
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") == "1" {
		var err error
		root, err = os.MkdirTemp("/var/tmp", "ar-systemd-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	sessionID := "44444444-4444-4444-8444-444444444444"
	userID := "11111111-1111-4111-8111-111111111111"
	accountID := "22222222-2222-4222-8222-222222222222"
	unit := "agent-remote-session-" + shortDigest(sessionID, 12) + ".service"
	result, code, status := "success", "1", "0"
	if unclean {
		result, code, status = "signal", "2", "9"
	}
	before := unitFields("active", "/system.slice/"+unit, "success", "0", "0")
	if !unclean {
		// Clean capture requires normal completion before forced cleanup, not its return status.
		before = strings.ReplaceAll(unitFields("active", "/system.slice/"+unit, "success", "1", "0"), "SubState=running", "SubState=exited")
	}
	command, _ := nativeStopCommands(t, before, unitFields("inactive", "", result, code, status), false)
	engine := NewEngine(EngineConfig{
		StateRoot: filepath.Join(root, "runtime"), WorkspaceRoot: filepath.Join(root, "workspaces"), AccountRoot: filepath.Join(root, "accounts"),
		SkillStateRoot: filepath.Join(root, "skills"), NodeID: "33333333-3333-4333-8333-333333333333",
		SystemctlPath: command, CgroupRoot: filepath.Join(root, "cgroups"), IPPath: writeTestCommand(t, "ip", "exit 0"),
	})
	if err := os.Mkdir(engine.config.CgroupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionRoot := filepath.Join(engine.config.StateRoot, "sessions", sessionID)
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := SessionSpec{
		Version: ProtocolVersion, Kind: "session", SessionID: sessionID, UserID: userID,
		Username: "ar-u-" + shortDigest(userID, 12), WorkspacePath: filepath.Join(engine.config.WorkspaceRoot, userID),
		AccountPath: filepath.Join(engine.config.AccountRoot, userID, "tool-accounts", "claude", accountID),
		SessionRoot: sessionRoot, RuntimeRoot: filepath.Dir(filepath.Dir(engine.config.ClaudeRuntimePath)), RuntimeCommand: "/opt/agent-remote/runtime/bin/claude",
		TmuxSessionName: "session-test", TmuxSocketPath: filepath.Join(sessionRoot, "tmux", "tmux.sock"), UnitName: unit,
		NetworkNamespace: "ar-" + shortDigest(sessionID, 10), RuntimeConfig: sessionRuntimeConfigFromEngine(engine.config),
		SkillSnapshotID: "55555555-5555-4555-8555-555555555555", BootID: currentBootID(), RuntimeUID: 12345, RuntimeGID: 12345,
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine.specPath(sessionID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	source := skillmanager.Manifest{Version: 1}
	digest, err := skillmanager.Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	binding := skillmanager.SnapshotBinding{
		UserID: userID, AccountID: accountID, NodeID: engine.config.NodeID, SessionID: sessionID, SnapshotID: spec.SkillSnapshotID,
		DirectoryEpoch: 1, LibraryGeneration: 1, InitialTreeDigest: digest,
		SystemReleases: testSkillSystemPins(),
	}
	if taskID != "" {
		binding.TaskID = taskID
		binding.PreparationDigest = strings.Repeat("c", 64)
	}
	open := func(context.Context, string) (io.ReadCloser, error) {
		return nil, errors.New("empty source has no objects")
	}
	if err := engine.prepareNativeSkillSnapshot(context.Background(), spec, binding, source, open); err != nil {
		t.Fatal(err)
	}
	return engine, spec, binding
}

func TestNativeSkillStopCapturesAndBlocksCleanupUntilServerRetention(t *testing.T) {
	engine, spec, binding := nativeSkillFixture(t, false)
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	file, err := bundle.OpenFile("work/learned", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("runtime learning")); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	result, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID})
	if err != nil || result["status"] != "stopped" || result["state_pending"] != true || result["skill_state"] != "local_durable" {
		t.Fatalf("stop did not distinguish process and data state: %#v, %v", result, err)
	}
	if _, err := os.Stat(engine.specPath(spec.SessionID)); err != nil {
		t.Fatal("pending state lost session spec", err)
	}
	first, err := skillmanager.ReadFinalization(bundle, binding)
	if err != nil || first.Unclean {
		t.Fatalf("clean capture failed: %#v, %v", first, err)
	}
	if _, err := engine.cleanupResources(context.Background(), map[string]any{"runtime_backend": "native", "session_ids": []any{spec.SessionID}}); err == nil || !strings.Contains(err.Error(), "state_pending") {
		t.Fatalf("cleanup accepted non-retained learning: %v", err)
	}
	second, err := skillmanager.ReadFinalization(bundle, binding)
	if err != nil || first != second {
		t.Fatalf("stop retry recaptured or reclassified learning: %#v, %v", second, err)
	}
	// This supplies an explicit test acknowledgement, not evidence of authenticated Server transfer.
	for _, states := range [][2]string{{"local_durable", "upload_pending"}, {"upload_pending", "persisted"}, {"persisted", "published"}} {
		if _, err := skillmanager.AdvanceFinalization(bundle, binding, states[0], states[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.SessionRoot); !os.IsNotExist(err) {
		t.Fatal("retained snapshot did not permit transient cleanup", err)
	}
	if _, err := bundle.Stat("work/learned"); err != nil {
		t.Fatal("session cleanup removed durable work", err)
	}
}

func TestNativeSkillRecoveryUsesRetainedBindingAfterSpecDisappears(t *testing.T) {
	engine, spec, binding := nativeSkillFixture(t, true)
	if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
		t.Fatal(err)
	}
	result, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID})
	if err != nil || result["state_pending"] != true || result["skill_unclean"] != true {
		t.Fatalf("missing spec lost recovery: %#v, %v", result, err)
	}
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if record, err := skillmanager.ReadFinalization(bundle, binding); err != nil || !record.Unclean {
		t.Fatalf("recovery did not preserve unclean content: %#v, %v", record, err)
	}
	for _, states := range [][2]string{{"local_durable", "upload_pending"}, {"upload_pending", "persisted_unclean"}, {"persisted_unclean", "detached"}} {
		if _, err := skillmanager.AdvanceFinalization(bundle, binding, states[0], states[1]); err != nil {
			t.Fatal(err)
		}
	}
	result, err = engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID})
	if err != nil || result["state_pending"] != false {
		t.Fatalf("terminal recovery failed without a spec: %#v, %v", result, err)
	}
	if _, err := os.Stat(spec.SessionRoot); !os.IsNotExist(err) {
		t.Fatal("retained recovery did not clean the transient session view", err)
	}
	if _, err := bundle.Stat("work"); err != nil {
		t.Fatal("recovery deleted persistent work", err)
	}
}

func TestNativeSkillWrongNodeOrMissingBindingNeverPermitsCleanup(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrong_node", true: "missing_binding"}[corrupt], func(t *testing.T) {
			engine, spec, _ := nativeSkillFixture(t, false)
			if corrupt {
				bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if err := bundle.Remove("runtime.json"); err != nil {
					t.Fatal(err)
				}
				_ = bundle.Close()
			} else {
				engine.config.NodeID = "77777777-7777-4777-8777-777777777777"
			}
			if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID}); err == nil {
				t.Fatal("invalid binding permitted session cleanup")
			}
			if _, err := os.Stat(engine.specPath(spec.SessionID)); err != nil {
				t.Fatal("invalid binding lost transient evidence", err)
			}
		})
	}
}

func TestNativeSkillCaptureFailureStillBlocksRelaunch(t *testing.T) {
	engine, spec, binding := nativeSkillFixture(t, false)
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if err := bundle.Symlink("/outside", "work/nonportable"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.finalizeNativeSkillSession(context.Background(), spec, nativeTermination{}); err == nil {
		t.Fatal("nonportable work unexpectedly finalized")
	}
	if record, err := skillmanager.ReadTermination(bundle, binding); err != nil || record.Unclean {
		t.Fatalf("capture failure lost clean termination evidence: %#v, %v", record, err)
	}
	if err := engine.mountNativeSkills(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "terminated") {
		t.Fatalf("terminated work was allowed to relaunch: %v", err)
	}
}
