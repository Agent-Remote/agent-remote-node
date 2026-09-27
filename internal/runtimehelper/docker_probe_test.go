package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDockerSandboxProbeRequiresEachRealCommand(t *testing.T) {
	for _, test := range []struct {
		name, script string
		available    bool
	}{
		{"supported", `printf 'Usage:\n  docker sandbox %s [OPTIONS]\n' "$2"`, true},
		{"removed-zero-exit", `printf '"docker sandbox" is deprecated and has been removed.\n'`, false},
		{"empty-zero-exit", "exit 0", false},
		{"generic-help", "printf 'Usage:\\n  docker sandbox [OPTIONS] COMMAND\\n'", false},
		{"missing-exec", `test "$2" != exec || exit 1; printf 'Usage:\n  docker sandbox %s [OPTIONS]\n' "$2"`, false},
		{"missing-rm", `test "$2" != rm || exit 1; printf 'Usage:\n  docker sandbox %s [OPTIONS]\n' "$2"`, false},
		{"failed-help", `printf 'Usage:\n  docker sandbox %s [OPTIONS]\n' "$2"; exit 1`, false},
		{"oversized", `printf 'Usage:\n  docker sandbox %s [OPTIONS]\n' "$2"; head -c 65537 /dev/zero`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := writeTestCommand(t, "docker", test.script)
			if got := dockerSandboxAvailable(binary); got != test.available {
				t.Fatalf("availability=%v, want %v", got, test.available)
			}
		})
	}
}

func TestDockerSandboxProbeHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if checkDockerSandboxCommands(ctx, writeTestCommand(t, "docker", "exit 0")) {
		t.Fatal("cancelled probe declared support")
	}
}

func supportedDockerCommand(t *testing.T) string {
	t.Helper()
	return writeTestCommand(t, "docker", `if [ "$1" = sandbox ] && [ "$3" = --help ]; then printf 'Usage:\n  docker sandbox %s [OPTIONS]\n' "$2"; fi`)
}

func TestRemovedDockerSandboxCannotMutateOrDiscardAccountState(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("trusted runtime specs require root on Linux")
	}
	root := t.TempDir()
	engine := NewEngine(EngineConfig{
		StateRoot: filepath.Join(root, "runtime"), AccountRoot: filepath.Join(root, "accounts"),
		SkillStateRoot: filepath.Join(root, "skills"), WorkspaceRoot: filepath.Join(root, "workspaces"),
		NodeID:           "33333333-3333-4333-8333-333333333333",
		DockerBinaryPath: writeTestCommand(t, "removed-docker", `printf '"docker sandbox" is deprecated and has been removed.\n'`),
		TmuxBinaryPath:   writeTestCommand(t, "tmux-must-not-run", `exit 99`),
	})
	payload := map[string]any{
		"user_id": "11111111-1111-4111-8111-111111111111", "tool_account_id": "22222222-2222-4222-8222-222222222222",
		"tool_type": "claude", "binding_id": "44444444-4444-4444-8444-444444444444",
		"session_id": "55555555-5555-4555-8555-555555555555", "workspace_id": "66666666-6666-4666-8666-666666666666",
		"tmux_session_name": "retained-tmux", "sandbox_name": "retained-sandbox",
	}
	for _, operation := range []string{"docker_prepare_account", "docker_start_session"} {
		_, err := engine.Execute(context.Background(), Request{Version: ProtocolVersion, RequestID: operation, Operation: operation, Payload: payload})
		if !errors.Is(err, errDockerSandboxUnavailable) || classifyError(err) != "CAPABILITY_UNAVAILABLE" {
			t.Fatal("removed plugin was admitted", operation, err)
		}
	}
	for _, path := range []string{engine.config.StateRoot, engine.config.AccountRoot, engine.config.WorkspaceRoot} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unavailable backend created runtime state", err)
		}
	}
	spec := DockerSessionSpec{Version: 1, Kind: "session", SessionID: payload["session_id"].(string), UserID: payload["user_id"].(string), TmuxSessionName: "retained-tmux", SandboxName: "retained-sandbox", RuntimeUID: 12345, RuntimeGID: 12345}
	if err := engine.saveDockerSessionSpec(spec); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(engine.dockerSessionSpecPath(spec.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"docker_stop_session", "cleanup_resources"} {
		request := map[string]any{"session_id": spec.SessionID, "session_ids": []any{spec.SessionID}, "runtime_backend": "docker_sandbox"}
		_, err := engine.Execute(context.Background(), Request{Version: ProtocolVersion, RequestID: operation, Operation: operation, Payload: request})
		if !errors.Is(err, errDockerSandboxUnavailable) {
			t.Fatal("stop accepted removed plugin", operation, err)
		}
		after, err := os.ReadFile(engine.dockerSessionSpecPath(spec.SessionID))
		if err != nil || string(after) != string(before) {
			t.Fatal("stop lost trusted writer evidence", err)
		}
	}
}

func TestDockerSandboxProbeBoundsStalledCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if checkDockerSandboxCommands(ctx, writeTestCommand(t, "docker-stalled", "sleep 30 & wait")) || time.Since(started) > 750*time.Millisecond {
		t.Fatal("stalled probe was not bounded")
	}
}

func TestInstalledRemovedDockerSandboxIsRejected(t *testing.T) {
	binary := os.Getenv("AGENT_REMOTE_TEST_REMOVED_DOCKER")
	if binary == "" {
		t.Skip("requires the installed removed-plugin stub")
	}
	if dockerSandboxAvailable(binary) {
		t.Fatal("removed installed plugin advertised support")
	}
}
