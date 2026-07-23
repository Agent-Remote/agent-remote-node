package runtimehelper

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

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

func TestBubblewrapUsesManagedLimitedTempDirectory(t *testing.T) {
	spec := SessionSpec{
		RuntimeRoot:    "/runtime",
		WorkspacePath:  "/workspaces/user/workspace",
		AccountPath:    "/accounts/user/account",
		SessionRoot:    "/runtime-state/session",
		Timezone:       "UTC",
		Locale:         "en_US.UTF-8",
		RuntimeCommand: "/opt/agent-remote/runtime/bin/claude",
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
		"-m", "u:ar-u-test:--x", "/var/lib/agent-remote", "/var/lib/agent-remote/users",
		"/var/lib/agent-remote/users/user_1", "/var/lib/agent-remote/users/user_1/tool-accounts",
		"/var/lib/agent-remote/users/user_1/tool-accounts/claude", "",
	}, "\n")
	if string(data) != want {
		t.Fatalf("unexpected setfacl arguments: %q", data)
	}
}

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
