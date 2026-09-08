package toolsessions

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDockerSandboxCommandsMountManagedEgoBrowserContextWithoutSecretValues(t *testing.T) {
	accountPath := "/var/lib/agent-remote/users/user/tool-accounts/claude/account"
	payload := CreatePayload{
		SandboxName: "session", ToolType: "claude", EgoBrowserEnabled: true,
		EgoBrowserWrapperPath:  "/opt/agent-remote/ego-browser/current/bin/ego-browser",
		EgoBrowserSkillPath:    "/opt/agent-remote/ego-browser/current/skill/ego-browser",
		EgoBrowserBrokerSocket: "/run/agent-remote-node/ego-browser-broker.sock",
		EgoBrowserBrokerNonce:  "secret-session-capability",
		EgoBrowserTaskSpace:    "agent-remote:session",
	}
	runtime := SandboxRuntime{
		UID: 1200, GID: 1200,
		Mounts: []string{
			"/opt/agent-remote/ego-browser/current/bin",
			"/opt/agent-remote/ego-browser/current/skill/ego-browser",
			"/run/agent-remote-node",
		},
		Environment: []string{
			"PATH=" + filepath.Dir(payload.EgoBrowserWrapperPath) + ":/usr/local/bin:/usr/bin:/bin",
			"EGO_BROWSER_WRAPPER_PATH=" + payload.EgoBrowserWrapperPath,
			"EGO_BROWSER_BROKER_SOCKET=" + payload.EgoBrowserBrokerSocket,
			"EGO_BROWSER_BROKER_NONCE=" + payload.EgoBrowserBrokerNonce,
		},
	}
	commands := append(
		sandboxCreateArgs("/workspace", accountPath, "", payload, runtime),
		sandboxExecCommand("docker", "/workspace", accountPath, "", payload, runtime)...,
	)
	joined := strings.Join(commands, "\x00")
	for _, required := range []string{
		"PATH", "EGO_BROWSER_WRAPPER_PATH", "EGO_BROWSER_BROKER_SOCKET", "EGO_BROWSER_BROKER_NONCE",
		filepath.Dir(payload.EgoBrowserWrapperPath), payload.EgoBrowserSkillPath,
		filepath.Dir(payload.EgoBrowserBrokerSocket), "1200:1200",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("Docker sandbox command omitted managed value %q: %#v", required, commands)
		}
	}
	if strings.Contains(joined, payload.EgoBrowserBrokerNonce) {
		t.Fatalf("Docker sandbox command exposed the broker nonce: %#v", commands)
	}
}

func TestPrepareInstallsManagedSkillsAndOwnsRuntimeFiles(t *testing.T) {
	workspaceRoot := t.TempDir()
	accountRoot := t.TempDir()
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		uid, gid = 12345, 12345
	}
	runtime := SandboxRuntime{UID: uid, GID: gid}
	result, err := Prepare(
		workspaceRoot,
		accountRoot,
		"docker",
		"agent-remote-missing-tmux",
		CreatePayload{
			SessionID: "session_1", ToolAccountID: "account_1", ToolType: "claude",
			UserID: "user_1", WorkspaceID: "workspace_1", TmuxSessionName: "tmux_1",
			SandboxName: "sandbox_1", RuntimeBackend: "docker_sandbox",
		},
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		result.MarkerPath,
		filepath.Join(result.AccountRemotePath, ".claude.json"),
		filepath.Join(result.AccountRemotePath, ".claude", "skills", "ego-browser", "SKILL.md"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected managed Docker file %s: %v", path, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok && (int(stat.Uid) != runtime.UID || int(stat.Gid) != runtime.GID) {
			t.Fatalf("unexpected owner for %s: %d:%d", path, stat.Uid, stat.Gid)
		}
	}
}

func TestDecodeCreatePayloadAcceptsDeviceControlForBothBackends(t *testing.T) {
	for _, backend := range []string{"native", "docker_sandbox"} {
		payload, err := DecodeCreatePayload(map[string]any{
			"session_id": "session", "tool_account_id": "account", "tool_type": "claude",
			"user_id": "user", "workspace_id": "workspace", "tmux_session_name": "tmux",
			"sandbox_name": "sandbox", "runtime_backend": backend,
			"device_control": map[string]any{"protocol_version": 1},
		})
		if err != nil || payload.DeviceControl == nil {
			t.Fatalf("%s device control was rejected: payload=%#v err=%v", backend, payload, err)
		}
	}
}

func TestDockerSandboxEnvironmentScrubsEveryEgoBrowserVariable(t *testing.T) {
	environ := []string{"PATH=/usr/bin", "EGO_BROWSER_FUTURE_SECRET=secret", "LANG=C"}
	got := clearManagedEnvironment(environ)
	if strings.Join(got, "\x00") != "PATH=/usr/bin\x00LANG=C" {
		t.Fatalf("unexpected scrubbed environment: %#v", got)
	}
}
