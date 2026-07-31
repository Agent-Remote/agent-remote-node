package runtimehelper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/devicecontrol"
)

func TestInitializeGitIndexFromHead(t *testing.T) {
	workspacePath := t.TempDir()
	runTestGit(t, workspacePath, "init")
	if err := os.WriteFile(filepath.Join(workspacePath, "tracked.txt"), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workspacePath, "add", "tracked.txt")
	runTestGit(t, workspacePath, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	indexPath := filepath.Join(workspacePath, ".git", "index")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := initializeGitIndex(workspacePath, runtimeIdentity{UID: os.Geteuid(), GID: os.Getegid()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatal(err)
	}
	output := runTestGit(t, workspacePath, "status", "--porcelain")
	if strings.TrimSpace(output) != "" {
		t.Fatalf("rebuilt index must match HEAD: %s", output)
	}
}

func runTestGit(t *testing.T, workspacePath string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", workspacePath}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return string(output)
}

func TestWireGuardSyncUsesValidatedRootOwnedConfig(t *testing.T) {
	stateRoot := t.TempDir()
	privateKeyPath := filepath.Join(t.TempDir(), "wireguard.key")
	privateKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	publicBytes := make([]byte, 32)
	publicBytes[0] = 1
	publicKey := base64.StdEncoding.EncodeToString(publicBytes)
	if err := os.WriteFile(privateKeyPath, []byte(privateKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capturePath := filepath.Join(t.TempDir(), "sync.conf")
	wgPath := writeTestCommand(t, "wg", `
[ "$1" = syncconf ] || exit 2
[ "$2" = agent-remote ] || exit 3
cp "$3" "$WG_CAPTURE"
`)
	t.Setenv("WG_CAPTURE", capturePath)
	engine := NewEngine(EngineConfig{
		StateRoot: stateRoot, WireGuardPrivateKey: privateKeyPath, WGBinaryPath: wgPath,
	})
	result, err := engine.wireGuardSync(context.Background(), map[string]any{
		"peers": []any{map[string]any{
			"public_key": publicKey, "allowed_ips": []any{"10.77.0.2/32"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["peer_count"] != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{privateKey, publicKey, "AllowedIPs = 10.77.0.2/32"} {
		if !strings.Contains(string(captured), expected) {
			t.Fatalf("sync config is missing %q", expected)
		}
	}
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary WireGuard config was not removed: %#v", entries)
	}
}

func TestParseRuntimePolicyAppliesDefaultsAndLowerLimits(t *testing.T) {
	policy, err := parseRuntimePolicy(map[string]any{
		"memory_high_bytes": float64(1 << 30),
		"memory_max_bytes":  float64(2 << 30),
		"cpu_quota_percent": float64(150),
		"tasks_max":         float64(256),
		"limit_nofile":      float64(4096),
		"tmpfs_size_bytes":  float64(512 << 20),
		"network_allowlist": []any{"10.23.4.8/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.MemoryMaxBytes != 2<<30 || policy.CPUQuotaPercent != 150 || policy.TmpfsSizeBytes != 512<<20 {
		t.Fatalf("unexpected policy: %#v", policy)
	}
	if !slices.Equal(policy.NetworkAllowlist, []string{"10.23.4.0/24"}) {
		t.Fatalf("allowlist was not normalized: %#v", policy.NetworkAllowlist)
	}
}

func TestCleanupResourcesIsIdempotentForMissingSession(t *testing.T) {
	engine := NewEngine(EngineConfig{StateRoot: t.TempDir()})
	result, err := engine.cleanupResources(context.Background(), map[string]any{
		"runtime_backend": "native",
		"session_ids":     []any{"session_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["cleaned_count"] != 1 {
		t.Fatalf("unexpected cleanup result: %#v", result)
	}
}

func TestDialSessionLoopbackRejectsUnmanagedTargets(t *testing.T) {
	engine := NewEngine(EngineConfig{StateRoot: t.TempDir()})
	for name, request := range map[string]Request{
		"version": {
			Version: ProtocolVersion + 1, RequestID: "request-1", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "session-1", "runtime_backend": "native", "port": 5173},
		},
		"request ID": {
			Version: ProtocolVersion, RequestID: "../request", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "session-1", "runtime_backend": "native", "port": 5173},
		},
		"backend": {
			Version: ProtocolVersion, RequestID: "request-1", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "session-1", "runtime_backend": "docker_sandbox", "port": 5173},
		},
		"port": {
			Version: ProtocolVersion, RequestID: "request-1", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "session-1", "runtime_backend": "native", "port": 0},
		},
		"unknown target": {
			Version: ProtocolVersion, RequestID: "request-1", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "session-1", "runtime_backend": "native", "port": 5173, "host": "169.254.169.254"},
		},
		"session ID": {
			Version: ProtocolVersion, RequestID: "request-1", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "../session", "runtime_backend": "native", "port": 5173},
		},
		"missing managed spec": {
			Version: ProtocolVersion, RequestID: "request-1", Operation: "dial_session_loopback",
			Payload: map[string]any{"session_id": "session-1", "runtime_backend": "native", "port": 5173},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if connection, err := engine.DialSessionLoopback(context.Background(), request); err == nil {
				connection.Close()
				t.Fatal("expected unmanaged target to be rejected")
			}
		})
	}
}

func TestSessionProcessExitRequiresSameBootMarker(t *testing.T) {
	sessionRoot := t.TempDir()
	spec := SessionSpec{SessionRoot: sessionRoot, BootID: "boot-1"}
	if err := os.WriteFile(processExitMarkerPath(spec), []byte("boot-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !sessionProcessExited(spec, false, "boot-1") {
		t.Fatal("same-boot process exit marker was not detected")
	}
	if sessionProcessExited(spec, true, "boot-1") {
		t.Fatal("active session must not be reported as exited")
	}
	if sessionProcessExited(spec, false, "boot-2") {
		t.Fatal("marker from a previous boot must not be treated as a normal exit")
	}
}

func TestDockerSessionProcessExitRequiresSameBoot(t *testing.T) {
	spec := DockerSessionSpec{BootID: "boot-1"}
	if !dockerSessionProcessExited(spec, false, "boot-1") {
		t.Fatal("same-boot Docker process exit was not detected")
	}
	if dockerSessionProcessExited(spec, true, "boot-1") {
		t.Fatal("active Docker session must not be reported as exited")
	}
	if dockerSessionProcessExited(spec, false, "boot-2") {
		t.Fatal("Docker session from a previous boot must not be treated as a normal exit")
	}
}

func TestDockerSessionSpecRoundTripAndRemoval(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("Docker session specs require root-owned runtime state on Linux")
	}
	engine := NewEngine(EngineConfig{StateRoot: t.TempDir()})
	spec := DockerSessionSpec{
		SessionID: "session_1", TmuxSessionName: "ar-claude-test",
		SandboxName: "agent-remote-claude-test", BootID: "boot-1",
	}
	if err := engine.saveDockerSessionSpec(spec); err != nil {
		t.Fatal(err)
	}
	loaded, err := engine.loadDockerSessionSpec(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != spec {
		t.Fatalf("unexpected Docker session spec: %#v", loaded)
	}
	if err := engine.removeDockerSessionSpec(spec.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.loadDockerSessionSpec(spec.SessionID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected removed Docker session spec, got %v", err)
	}
}

func TestCleanupTempIsIdempotentWhenDirectoryIsNotMounted(t *testing.T) {
	stateRoot := t.TempDir()
	sessionRoot := filepath.Join(stateRoot, "sessions", "session_1")
	if err := os.MkdirAll(filepath.Join(sessionRoot, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	umountPath := writeTestCommand(t, "umount", "exit 32")
	mountpointPath := writeTestCommand(t, "mountpoint", "exit 1")
	engine := NewEngine(EngineConfig{
		StateRoot:      stateRoot,
		UmountPath:     umountPath,
		MountpointPath: mountpointPath,
	})
	if err := engine.cleanupTemp(context.Background(), SessionSpec{SessionRoot: sessionRoot}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupTempPreservesUnmountFailureForMountedDirectory(t *testing.T) {
	stateRoot := t.TempDir()
	sessionRoot := filepath.Join(stateRoot, "sessions", "session_1")
	if err := os.MkdirAll(filepath.Join(sessionRoot, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	umountPath := writeTestCommand(t, "umount", "exit 32")
	mountpointPath := writeTestCommand(t, "mountpoint", "exit 0")
	engine := NewEngine(EngineConfig{
		StateRoot:      stateRoot,
		UmountPath:     umountPath,
		MountpointPath: mountpointPath,
	})
	if err := engine.cleanupTemp(context.Background(), SessionSpec{SessionRoot: sessionRoot}); err == nil {
		t.Fatal("expected active mount unmount failure")
	}
}

func writeTestCommand(t *testing.T, name string, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCleanupResourcesRejectsImplicitScope(t *testing.T) {
	engine := NewEngine(EngineConfig{StateRoot: t.TempDir()})
	for _, payload := range []map[string]any{
		{"runtime_backend": "native"},
		{"runtime_backend": "docker_sandbox", "session_ids": []any{"session_1"}},
	} {
		if _, err := engine.cleanupResources(context.Background(), payload); err == nil {
			t.Fatalf("expected payload to be rejected: %#v", payload)
		}
	}
}

func TestParseRuntimePolicyRejectsPrivilegeExpansion(t *testing.T) {
	invalid := []map[string]any{
		{"memory_max_bytes": float64((4 << 30) + 1)},
		{"cpu_quota_percent": float64(201)},
		{"memory_high_bytes": float64(3 << 30), "memory_max_bytes": float64(2 << 30)},
		{"network_allowlist": []any{"not-a-cidr"}},
		{"network_allowlist": []any{"2001:db8::/32"}},
	}
	for _, raw := range invalid {
		if _, err := parseRuntimePolicy(raw); err == nil {
			t.Fatalf("expected policy to be rejected: %#v", raw)
		}
	}
}

func TestClaudeBindingArgvUsesTemplateCommand(t *testing.T) {
	tests := []struct {
		name    string
		command []any
		want    []string
	}{
		{name: "full onboarding", command: []any{"claude"}, want: []string{}},
		{name: "arguments", command: []any{"claude", "--model", "opus"}, want: []string{"--model", "opus"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := claudeBindingArgv(map[string]any{
				"template": map[string]any{"command": test.command},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("unexpected binding arguments: got %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestClaudeBindingArgvRejectsInvalidTemplateCommand(t *testing.T) {
	tests := []map[string]any{
		{},
		{"template": map[string]any{}},
		{"template": map[string]any{"command": []any{}}},
		{"template": map[string]any{"command": []any{"bash"}}},
		{"template": map[string]any{"command": []any{"claude", 1}}},
		{"template": map[string]any{"command": []any{"claude", "bad\x00argument"}}},
	}
	for _, payload := range tests {
		if _, err := claudeBindingArgv(payload); err == nil {
			t.Fatalf("expected payload to be rejected: %#v", payload)
		}
	}
}

func TestBubblewrapUsesManagedLimitedTempDirectory(t *testing.T) {
	spec := SessionSpec{
		RuntimeRoot:                    "/runtime",
		WorkspacePath:                  "/workspaces/user/workspace",
		AccountPath:                    "/accounts/user/account",
		DeveloperCredentialProfilePath: "/accounts/user/developer-profile",
		SSHAgentDirectory:              "/runtime-state/session/ssh-agent",
		SessionRoot:                    "/runtime-state/session",
		Timezone:                       "UTC",
		Locale:                         "en_US.UTF-8",
		RuntimeCommand:                 "/opt/agent-remote/runtime/bin/claude",
	}
	args := bubblewrapArgs(EngineConfig{}, spec)
	if slices.Contains(args, "--tmpfs") {
		t.Fatalf("bubblewrap must not replace the quota-limited temp mount: %#v", args)
	}
	found := false
	for index := 0; index+2 < len(args); index++ {
		if args[index] == "--bind" && args[index+1] == "/runtime-state/session/tmp" && args[index+2] == "/tmp" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("managed temp directory was not bound: %#v", args)
	}
	assertArgumentSequence(t, args, "--bind", "/accounts/user/account/.claude", "/home/runtime/.claude")
	assertArgumentSequence(t, args, "--bind", "/accounts/user/account/.claude.json", "/home/runtime/.claude/.claude.json")
	assertArgumentSequence(t, args, "--setenv", "CLAUDE_CONFIG_DIR", "/home/runtime/.claude")
	assertArgumentSequence(t, args, "--setenv", "PATH", "/opt/agent-remote/runtime/bin:/usr/local/bin:/usr/bin:/bin")
	assertArgumentSequence(t, args, "--ro-bind", "/runtime-state/session/passwd", "/etc/passwd")
	assertArgumentSequence(t, args, "--ro-bind", "/runtime-state/session/group", "/etc/group")
	assertArgumentSequence(t, args, "--bind", "/accounts/user/developer-profile", "/developer-profile")
	assertArgumentSequence(t, args, "--setenv", "GIT_CONFIG_GLOBAL", "/developer-profile/home/.gitconfig")
	assertArgumentSequence(t, args, "--setenv", "GH_CONFIG_DIR", "/developer-profile/gh")
	assertArgumentSequence(t, args, "--bind", "/runtime-state/session/ssh-agent", "/run/agent-remote/ssh-agent")
	assertArgumentSequence(t, args, "--setenv", "SSH_AUTH_SOCK", "/run/agent-remote/ssh-agent/agent.sock")
}

// TestManagedDeviceControlUsesOnlyFixedMCPAndSandboxPaths verifies fixed managed runtime paths.
func TestManagedDeviceControlUsesOnlyFixedMCPAndSandboxPaths(t *testing.T) {
	sessionID := "123e4567-e89b-42d3-a456-426614174000"
	arguments, err := managedDeviceControlArgv(sessionID, []string{"--model", "opus"})
	if err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 9 || arguments[0] != "--setting-sources" || arguments[1] != "" ||
		arguments[2] != "--settings" || arguments[4] != "--strict-mcp-config" ||
		arguments[5] != "--mcp-config" {
		t.Fatalf("unexpected managed arguments: %#v", arguments)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(arguments[3]), &settings); err != nil {
		t.Fatal(err)
	}
	hooks := settings["hooks"].(map[string]any)
	for event, notification := range map[string]string{
		"Stop": "turn-stop", "StopFailure": "turn-stop", "SessionEnd": "session-end",
	} {
		entries := hooks[event].([]any)
		handlers := entries[0].(map[string]any)["hooks"].([]any)
		handler := handlers[0].(map[string]any)
		if handler["command"] != "/opt/agent-remote/device/bin/agent-remote-device-proxy" ||
			handler["timeout"] != float64(5) {
			t.Fatalf("unexpected %s hook: %#v", event, handler)
		}
		wantArgs := []any{"--notify", notification, "--lifecycle-socket", "/tmp/lifecycle.sock"}
		if !slices.Equal(handler["args"].([]any), wantArgs) {
			t.Fatalf("unexpected %s hook arguments: %#v", event, handler["args"])
		}
	}
	var configuration map[string]any
	if err := json.Unmarshal([]byte(arguments[6]), &configuration); err != nil {
		t.Fatal(err)
	}
	servers := configuration["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Fatalf("unexpected MCP servers: %#v", servers)
	}
	device := servers["agent-remote-device"].(map[string]any)
	if device["command"] != "/opt/agent-remote/device/bin/agent-remote-device-proxy" {
		t.Fatalf("unexpected proxy command: %#v", device)
	}
	if got := device["args"].([]any); !slices.Contains(got, "/tmp/lifecycle.sock") {
		t.Fatalf("managed MCP arguments omit the lifecycle socket: %#v", got)
	}
	encoded := arguments[6]
	for _, forbidden := range []string{"http://", "https://", "ws://", "wss://", "token", "secret"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("managed MCP configuration contains %q: %s", forbidden, encoded)
		}
	}
	for _, override := range [][]string{
		{"--mcp-config", "{}"}, {"--mcp-config={}"}, {"--strict-mcp-config=false"},
		{"--settings", "{}"}, {"--settings={}"}, {"--setting-sources", "project"},
		{"--setting-sources=local"}, {"--safe-mode"}, {"--safe-mode=true"}, {"--bare"},
	} {
		if _, err := managedDeviceControlArgv(sessionID, override); err == nil {
			t.Fatalf("expected override rejection: %#v", override)
		}
	}

	spec := SessionSpec{
		RuntimeRoot: "/runtime", WorkspacePath: "/workspace-host", AccountPath: "/account-host",
		SessionRoot: "/runtime-state/session", Timezone: "UTC", Locale: "en_US.UTF-8",
		RuntimeCommand:               "/opt/agent-remote/runtime/bin/claude",
		DeviceControlProtocolVersion: 1,
		DeviceControlDirectory:       "/runtime-state/session/device-control",
		DeviceProxyPath:              "/opt/agent-remote/device/current/bin/agent-remote-device-proxy",
	}
	bubblewrap := bubblewrapArgs(EngineConfig{}, spec)
	assertArgumentSequence(t, bubblewrap, "--ro-bind", spec.DeviceControlDirectory, "/run/agent-remote/device")
	assertArgumentSequence(t, bubblewrap, "--ro-bind", spec.DeviceProxyPath, "/opt/agent-remote/device/bin/agent-remote-device-proxy")
}

// TestDeviceControlContextUpdatesPreserveGenerationStateAndClearSafely verifies generation-bound context updates.
func TestDeviceControlContextUpdatesPreserveGenerationStateAndClearSafely(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("managed device-control specs require root-owned runtime state on Linux")
	}
	engine, spec := managedDeviceControlTestEngine(t)
	now := time.Now().UTC()
	payload := map[string]any{
		"protocol_version":  1,
		"user_id":           spec.UserID,
		"device_id":         "123e4567-e89b-42d3-a456-426614174002",
		"tool_session_id":   spec.SessionID,
		"device_session_id": "123e4567-e89b-42d3-a456-426614174003",
		"node_id":           "123e4567-e89b-42d3-a456-426614174004",
		"platform":          "macos",
		"generation":        uint64(1),
		"lease_until":       now.Add(60 * time.Second).Format(time.RFC3339Nano),
	}
	if _, err := engine.updateDeviceControlContext(payload); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(spec.DeviceControlDirectory, "context.json")
	context, err := loadManagedDeviceContext(contextPath, spec)
	if err != nil {
		t.Fatal(err)
	}
	if context.NextSequence != 1 || context.CurrentScreenshotGeneration != 0 {
		t.Fatalf("unexpected initial context state: %#v", context)
	}
	context.Generation = devicecontrol.MaximumDeviceSessionGeneration
	if err := writeManagedDeviceContext(contextPath, context, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManagedDeviceContext(contextPath, spec); err == nil {
		t.Fatal("expected terminal-only generation context rejection")
	}
	context.Generation = 1
	if err := writeManagedDeviceContext(contextPath, context, spec); err != nil {
		t.Fatal(err)
	}
	context.NextSequence = 7
	context.CurrentScreenshotGeneration = 5
	if err := writeManagedDeviceContext(contextPath, context, spec); err != nil {
		t.Fatal(err)
	}
	payload["lease_until"] = now.Add(90 * time.Second).Format(time.RFC3339Nano)
	if _, err := engine.updateDeviceControlContext(payload); err != nil {
		t.Fatal(err)
	}
	renewed, err := loadManagedDeviceContext(contextPath, spec)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.NextSequence != 7 || renewed.CurrentScreenshotGeneration != 5 {
		t.Fatalf("lease renewal reset action state: %#v", renewed)
	}

	olderLease := mapsClone(payload)
	olderLease["lease_until"] = now.Add(30 * time.Second).Format(time.RFC3339Nano)
	if _, err := engine.updateDeviceControlContext(olderLease); err == nil {
		t.Fatal("expected a backwards lease update to be rejected")
	}
	changedBinding := mapsClone(payload)
	changedBinding["device_id"] = "123e4567-e89b-42d3-a456-426614174005"
	if _, err := engine.updateDeviceControlContext(changedBinding); err == nil {
		t.Fatal("expected a same-generation binding change to be rejected")
	}

	payload["generation"] = uint64(2)
	payload["lease_until"] = now.Add(120 * time.Second).Format(time.RFC3339Nano)
	if _, err := engine.updateDeviceControlContext(payload); err != nil {
		t.Fatal(err)
	}
	secondGeneration, err := loadManagedDeviceContext(contextPath, spec)
	if err != nil {
		t.Fatal(err)
	}
	if secondGeneration.NextSequence != 1 || secondGeneration.CurrentScreenshotGeneration != 0 {
		t.Fatalf("new generation did not reset action state: %#v", secondGeneration)
	}
	clearPayload := map[string]any{
		"device_session_id": payload["device_session_id"],
		"tool_session_id":   spec.SessionID,
		"generation":        uint64(2),
		"inclusive":         false,
	}
	result, err := engine.clearDeviceControlContext(clearPayload)
	if err != nil {
		t.Fatal(err)
	}
	if result["removed"] != false {
		t.Fatalf("activation retry removed its own generation: %#v", result)
	}
	clearPayload["inclusive"] = true
	result, err = engine.clearDeviceControlContext(clearPayload)
	if err != nil {
		t.Fatal(err)
	}
	if result["removed"] != true {
		t.Fatalf("terminal clear did not remove the context: %#v", result)
	}
}

func managedDeviceControlTestEngine(t *testing.T) (Engine, SessionSpec) {
	t.Helper()
	stateRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	accountRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	proxyPath := filepath.Join(t.TempDir(), "agent-remote-device-proxy")
	config := EngineConfig{
		StateRoot: stateRoot, WorkspaceRoot: workspaceRoot, AccountRoot: accountRoot,
		ClaudeRuntimePath: filepath.Join(runtimeRoot, "bin", "claude"), DeviceProxyPath: proxyPath,
	}
	engine := NewEngine(config)
	sessionID := "123e4567-e89b-42d3-a456-426614174001"
	userID := "123e4567-e89b-42d3-a456-426614174000"
	sessionRoot := filepath.Join(stateRoot, "sessions", sessionID)
	deviceDirectory := filepath.Join(sessionRoot, "device-control")
	if err := os.MkdirAll(deviceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	arguments, err := managedDeviceControlArgv(sessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := SessionSpec{
		Version: ProtocolVersion, Kind: "session", SessionID: sessionID, UserID: userID,
		Username: "ar-u-" + shortDigest(userID, 12), WorkspacePath: filepath.Join(workspaceRoot, "project"),
		AccountPath: filepath.Join(accountRoot, "account"), SessionRoot: sessionRoot,
		RuntimeRoot:    filepath.Clean(filepath.Join(filepath.Dir(config.ClaudeRuntimePath), "..")),
		RuntimeCommand: "/opt/agent-remote/runtime/bin/claude", Argv: arguments,
		DeviceControlProtocolVersion: 1, DeviceControlDirectory: deviceDirectory, DeviceProxyPath: proxyPath,
		Timezone: "UTC", Locale: "en_US.UTF-8", TmuxSessionName: "ar-native-test",
		TmuxSocketPath:   filepath.Join(sessionRoot, "tmux", "tmux.sock"),
		UnitName:         "agent-remote-session-" + shortDigest(sessionID, 12) + ".service",
		NetworkNamespace: "ar-" + shortDigest(sessionID, 10), RuntimeUID: os.Geteuid(), RuntimeGID: os.Getegid(),
	}
	if err := engine.saveSpec(spec); err != nil {
		t.Fatal(err)
	}
	return engine, spec
}

func mapsClone(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func TestRuntimeIdentityFilesRemainReadableWithRestrictiveExistingModes(t *testing.T) {
	sessionRoot := t.TempDir()
	for _, name := range []string{"passwd", "group"} {
		if err := os.WriteFile(filepath.Join(sessionRoot, name), []byte("stale\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	identity := runtimeIdentity{Username: "ar-u-test", UID: 996, GID: 994}
	if err := writeRuntimeIdentityFiles(sessionRoot, identity); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"passwd", "group"} {
		info, err := os.Stat(filepath.Join(sessionRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("%s mode = %o, want 644", name, info.Mode().Perm())
		}
	}
	passwd, err := os.ReadFile(filepath.Join(sessionRoot, "passwd"))
	if err != nil {
		t.Fatal(err)
	}
	if string(passwd) != "ar-u-test:x:996:994:agent-remote runtime:/home/runtime:/usr/sbin/nologin\n" {
		t.Fatalf("unexpected passwd contents: %q", passwd)
	}
}

func TestRenderGitConfigQuotesIdentityValues(t *testing.T) {
	config, err := renderGitConfig(map[string]any{
		"user_name":  `A "Quoted" User`,
		"user_email": `user\\alias@example.com`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`name = "A \"Quoted\" User"`, `email = "user\\\\alias@example.com"`} {
		if !strings.Contains(config, expected) {
			t.Fatalf("git config is missing %q: %s", expected, config)
		}
	}
	if _, err := renderGitConfig(map[string]any{"user_name": "unsafe\n[core]"}); err == nil {
		t.Fatal("expected multiline Git identity to be rejected")
	}
}

func TestSSHAgentProxyForwardsUnixConnections(t *testing.T) {
	testRoot, err := os.MkdirTemp("/tmp", "agent-remote-agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	upstreamPath := filepath.Join(testRoot, "upstream.sock")
	upstream, err := net.ListenUnix("unix", &net.UnixAddr{Name: upstreamPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		connection, acceptErr := upstream.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		request := make([]byte, 4)
		if _, readErr := io.ReadFull(connection, request); readErr == nil && string(request) == "ping" {
			_, _ = connection.Write([]byte("pong"))
		}
	}()

	directory := filepath.Join(testRoot, "proxy")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy, err := startSSHAgentProxy(directory, upstreamPath, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client, err := net.Dial("unix", filepath.Join(directory, "agent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatalf("unexpected proxy response %q", response)
	}
}

func assertArgumentSequence(t *testing.T, args []string, expected ...string) {
	t.Helper()
	for index := 0; index+len(expected) <= len(args); index++ {
		if slices.Equal(args[index:index+len(expected)], expected) {
			return
		}
	}
	t.Fatalf("expected argument sequence %#v in %#v", expected, args)
}

func TestGrantSpecAccessAddsTraverseACLToStateParents(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "setfacl.log")
	setfaclPath := filepath.Join(t.TempDir(), "setfacl")
	if err := os.WriteFile(setfaclPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ACL_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACL_LOG", logPath)
	engine := NewEngine(EngineConfig{StateRoot: root, SetfaclPath: setfaclPath})
	if err := engine.grantSpecAccess(SessionSpec{Username: "ar-u-test"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"-m", "u:ar-u-test:--x", root, filepath.Join(root, "sessions"), ""}, "\n")
	if string(data) != want {
		t.Fatalf("unexpected setfacl arguments: %q", data)
	}
}

func TestGrantManagedTraverseAddsOnlyManagedParentDirectories(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "setfacl.log")
	setfaclPath := filepath.Join(t.TempDir(), "setfacl")
	if err := os.WriteFile(setfaclPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ACL_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACL_LOG", logPath)
	engine := NewEngine(EngineConfig{
		WorkspaceRoot: "/var/lib/agent-remote/users", AccountRoot: "/var/lib/agent-remote/users", SetfaclPath: setfaclPath,
	})
	path := "/var/lib/agent-remote/users/user_1/tool-accounts/claude/account_1"
	if err := engine.grantManagedTraverse(path, "ar-u-test"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"-m", "u:ar-u-test:--x,u:agent-remote:--x", "/var/lib/agent-remote", "/var/lib/agent-remote/users",
		"/var/lib/agent-remote/users/user_1", "/var/lib/agent-remote/users/user_1/tool-accounts",
		"/var/lib/agent-remote/users/user_1/tool-accounts/claude", "",
	}, "\n")
	if string(data) != want {
		t.Fatalf("unexpected setfacl arguments: %q", data)
	}
}

func TestNormalizeWorkspaceModePreservesOnlyOwnerExecutableState(t *testing.T) {
	root := t.TempDir()
	normalPath := filepath.Join(root, "normal.txt")
	executablePath := filepath.Join(root, "script.sh")
	if err := os.WriteFile(normalPath, []byte("normal"), 0o670); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("script"), 0o760); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, normalPath, executablePath} {
		entry, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := normalizeWorkspaceMode(path, fileInfoDirEntry{entry}); err != nil {
			t.Fatal(err)
		}
	}
	assertMode := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("unexpected mode for %s: got %o, want %o", path, got, want)
		}
	}
	assertMode(root, 0o770)
	assertMode(normalPath, 0o660)
	assertMode(executablePath, 0o770)
}

func TestClearDataACLRemovesAccessAndDefaultEntries(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "setfacl.log")
	setfaclPath := writeTestCommand(t, "setfacl", `printf '%s\n' "$*" >> "$ACL_LOG"`)
	t.Setenv("ACL_LOG", logPath)
	engine := NewEngine(EngineConfig{SetfaclPath: setfaclPath})
	if err := engine.clearDataACL("/workspace"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "-R -b /workspace\n-R -k /workspace\n"; got != want {
		t.Fatalf("unexpected setfacl calls: got %q, want %q", got, want)
	}
}

// TestDeviceControlACLGrantsRuntimeReadOnlyAndNodeSocketAccess verifies the least-privilege ACL layout.
func TestDeviceControlACLGrantsRuntimeReadOnlyAndNodeSocketAccess(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "setfacl.log")
	setfaclPath := writeTestCommand(t, "setfacl", `printf '%s\n' "$*" > "$ACL_LOG"`)
	t.Setenv("ACL_LOG", logPath)
	engine := NewEngine(EngineConfig{SetfaclPath: setfaclPath, NodeUser: "agent-remote"})
	if err := engine.applyDeviceControlACL(directory, "ar-u-test"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "-m u:ar-u-test:r-x,u:agent-remote:rwx " + directory + "\n"
	if string(data) != want {
		t.Fatalf("unexpected device-control ACL: got %q, want %q", data, want)
	}
}

type fileInfoDirEntry struct {
	os.FileInfo
}

func (entry fileInfoDirEntry) Type() os.FileMode          { return entry.Mode().Type() }
func (entry fileInfoDirEntry) Info() (os.FileInfo, error) { return entry.FileInfo, nil }
func (entry fileInfoDirEntry) Name() string               { return entry.FileInfo.Name() }
func (entry fileInfoDirEntry) IsDir() bool                { return entry.FileInfo.IsDir() }

func TestWaitForSessionReadyRequiresStableTmuxSession(t *testing.T) {
	binDir := t.TempDir()
	systemctlPath := filepath.Join(binDir, "systemctl")
	tmuxPath := filepath.Join(binDir, "tmux")
	for path, body := range map[string]string{
		systemctlPath: "#!/bin/sh\necho active\n",
		tmuxPath:      "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewEngine(EngineConfig{SystemctlPath: systemctlPath, TmuxBinaryPath: tmuxPath})
	err := engine.waitForSessionReady(context.Background(), SessionSpec{
		UnitName: "agent-remote-session-test.service", TmuxSocketPath: "/tmp/test.sock", TmuxSessionName: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWaitForSessionReadyRejectsInactiveUnit(t *testing.T) {
	binDir := t.TempDir()
	systemctlPath := filepath.Join(binDir, "systemctl")
	if err := os.WriteFile(systemctlPath, []byte("#!/bin/sh\necho inactive\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(EngineConfig{SystemctlPath: systemctlPath})
	err := engine.waitForSessionReady(context.Background(), SessionSpec{UnitName: "agent-remote-session-test.service"})
	if err == nil || !strings.Contains(err.Error(), "became inactive") {
		t.Fatalf("expected inactive unit error, got %v", err)
	}
}

func TestReplaceEnvironmentOverridesRuntimeShell(t *testing.T) {
	environ := replaceEnvironment([]string{"PATH=/usr/bin", "SHELL=/usr/sbin/nologin"}, "SHELL", "/bin/sh")
	if !slices.Equal(environ, []string{"PATH=/usr/bin", "SHELL=/bin/sh"}) {
		t.Fatalf("unexpected environment: %#v", environ)
	}
}

func TestParseNFTForwardChainsFindsIPv4Hooks(t *testing.T) {
	data := []byte(`{"nftables":[{"metainfo":{"json_schema_version":1}},{"chain":{"family":"ip","table":"filter","name":"FORWARD","hook":"forward"}},{"chain":{"family":"inet","table":"firewall","name":"forward","hook":"forward"}},{"chain":{"family":"ip6","table":"filter","name":"FORWARD","hook":"forward"}},{"chain":{"family":"ip","table":"filter","name":"INPUT","hook":"input"}}]}`)
	chains, err := parseNFTForwardChains(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []nftForwardChain{{Family: "ip", Table: "filter", Name: "FORWARD"}, {Family: "inet", Table: "firewall", Name: "forward"}}
	if !slices.Equal(chains, want) {
		t.Fatalf("unexpected forward chains: %#v", chains)
	}
}

func TestNamespaceFirewallAllowsSessionLoopbackBeforePrivateRejects(t *testing.T) {
	commands := namespaceFirewallCommands(SessionSpec{})
	loopbackAllow := []string{"add", "rule", "inet", "agent_remote", "output", "oifname", "lo", "accept"}
	loopbackReject := []string{"add", "rule", "inet", "agent_remote", "output", "ip", "daddr", "127.0.0.0/8", "reject"}
	privateReject := []string{"add", "rule", "inet", "agent_remote", "output", "ip", "daddr", "10.0.0.0/8", "reject"}
	allowIndex := slices.IndexFunc(commands, func(command []string) bool { return slices.Equal(command, loopbackAllow) })
	loopbackRejectIndex := slices.IndexFunc(commands, func(command []string) bool { return slices.Equal(command, loopbackReject) })
	privateRejectIndex := slices.IndexFunc(commands, func(command []string) bool { return slices.Equal(command, privateReject) })
	if allowIndex < 0 || loopbackRejectIndex < 0 || allowIndex >= loopbackRejectIndex {
		t.Fatalf("session loopback must be allowed before private-range rejects: %#v", commands)
	}
	if privateRejectIndex < 0 {
		t.Fatalf("private network rejects must remain enabled: %#v", commands)
	}
}

func TestApplyNamespaceFirewallRunsAllCommands(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ip.log")
	ipPath := writeTestCommand(t, "ip", `printf '%s\n' "$*" >> "$IP_LOG"`)
	t.Setenv("IP_LOG", logPath)
	engine := NewEngine(EngineConfig{IPPath: ipPath, NFTPath: "/test/nft"})
	spec := SessionSpec{NetworkNamespace: "agent-remote-test"}

	if err := engine.applyNamespaceFirewall(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != len(namespaceFirewallCommands(spec)) {
		t.Fatalf("expected %d firewall commands, got %d: %q", len(namespaceFirewallCommands(spec)), len(lines), output)
	}
	if lines[0] != "netns exec agent-remote-test /test/nft add table inet agent_remote" {
		t.Fatalf("unexpected first firewall command: %q", lines[0])
	}
}

func TestApplyNamespaceFirewallReturnsCommandFailure(t *testing.T) {
	ipPath := writeTestCommand(t, "ip", `echo "nft unavailable" >&2; exit 42`)
	engine := NewEngine(EngineConfig{IPPath: ipPath, NFTPath: "/test/nft"})

	err := engine.applyNamespaceFirewall(context.Background(), SessionSpec{NetworkNamespace: "agent-remote-test"})
	if err == nil || !strings.Contains(err.Error(), "nft unavailable") {
		t.Fatalf("expected firewall command failure, got %v", err)
	}
}

func TestSaveSpecMetadataRemainsReadableWithRestrictiveUmask(t *testing.T) {
	root := t.TempDir()
	sessionRoot := filepath.Join(root, "sessions", "session_1")
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	previousUmask := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previousUmask) })
	engine := NewEngine(EngineConfig{StateRoot: root, DNSResolvers: []string{"1.1.1.1"}})
	if err := engine.saveSpec(SessionSpec{SessionID: "session_1", SessionRoot: sessionRoot, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resolv.conf", "timezone"} {
		info, err := os.Stat(filepath.Join(sessionRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("%s has mode %o", name, info.Mode().Perm())
		}
	}
}
