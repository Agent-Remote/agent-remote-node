package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
)

func TestConfigureWireGuardUsesControlPlaneHost(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		ServerURL: "https://64-81-112-77.sslip.io",
		NodeID:    "node_1",
		NodeToken: "node_token",
	}.WithDefaults()
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := configureWireGuard([]string{
		"--config", configPath,
		"--public-key", publicKey,
		"--version", "0.0.4-fix.3",
	}); err != nil {
		t.Fatal(err)
	}
	configured, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if configured.WireGuardEndpoint != "64-81-112-77.sslip.io:51820" || configured.WireGuardAddress != "10.77.0.1/24" {
		t.Fatalf("unexpected WireGuard config: %#v", configured)
	}
	if configured.Version != "0.0.4-fix.3" {
		t.Fatalf("node version was not refreshed: %q", configured.Version)
	}
}

func TestSplitCommaList(t *testing.T) {
	got := splitCommaList("native, docker_sandbox, ,native")
	want := []string{"native", "docker_sandbox", "native"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected list: %#v", got)
	}
}

func TestRegisterReusesExistingTokenAndRefreshesSystemLayout(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.json")
	existing := config.Config{
		ServerURL:              server.URL,
		NodeID:                 "node_1",
		NodeToken:              "node_existing",
		AllowedRuntimeBackends: []string{"docker_sandbox"},
	}.WithDefaults()
	if err := config.Save(configPath, existing); err != nil {
		t.Fatal(err)
	}
	if err := register([]string{
		"--config", configPath,
		"--server-url", server.URL,
		"--node-id", "node_1",
		"--registration-token", "already_used",
		"--runtime-backends", "native",
		"--system-install",
	}); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("existing registration unexpectedly called the control plane %d times", requests)
	}
	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.NodeToken != "node_existing" {
		t.Fatalf("existing node token was replaced: %q", updated.NodeToken)
	}
	if !reflect.DeepEqual(updated.AllowedRuntimeBackends, []string{"native"}) {
		t.Fatalf("runtime backends were not refreshed: %#v", updated.AllowedRuntimeBackends)
	}
	if updated.LedgerPath != "/var/lib/agent-remote-node/ledger.json" {
		t.Fatalf("system layout was not applied: %#v", updated)
	}
}

func TestApplySystemInstallPaths(t *testing.T) {
	cfg := config.Config{}
	applySystemInstallPaths(&cfg, "/opt/agent", "/srv/node", "/srv/data", "/opt/claude", "/opt/device-proxy")
	if cfg.LedgerPath != "/srv/node/ledger.json" {
		t.Fatalf("unexpected ledger path %q", cfg.LedgerPath)
	}
	if cfg.SSHAuthorizedKeysPath != "/srv/node/authorized_keys" {
		t.Fatalf("unexpected authorized keys path %q", cfg.SSHAuthorizedKeysPath)
	}
	if cfg.AttachBinaryPath != "/opt/agent/bin/agent-remote-attach" {
		t.Fatalf("unexpected attach path %q", cfg.AttachBinaryPath)
	}
	if cfg.WorkspaceRoot != "/srv/data/users" || cfg.AccountRoot != cfg.WorkspaceRoot {
		t.Fatalf("unexpected managed data paths: %#v", cfg)
	}
	if cfg.BrowserRoot != "/srv/data/browser-sessions" {
		t.Fatalf("unexpected browser root %q", cfg.BrowserRoot)
	}
	if cfg.RuntimeBinaryPath != "/opt/agent/bin/agent-remote-runtime" {
		t.Fatalf("unexpected runtime path %q", cfg.RuntimeBinaryPath)
	}
	if cfg.ClaudeRuntimePath != "/opt/claude" {
		t.Fatalf("unexpected Claude path %q", cfg.ClaudeRuntimePath)
	}
	if cfg.DeviceProxyPath != "/opt/device-proxy" {
		t.Fatalf("unexpected device proxy path %q", cfg.DeviceProxyPath)
	}
}

func TestResolveInstallServerURLCanonicalizesAndRejectsAmbiguousOrigins(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "loopback http", value: "http://127.0.0.1:80/", want: "http://127.0.0.1"},
		{name: "localhost", value: "HTTP://LOCALHOST:8080", want: "http://localhost:8080"},
		{name: "https default port", value: "https://Example.COM:443/", want: "https://example.com"},
		{name: "ipv6 compressed", value: "https://[0:0:0:0:0:0:0:1]:443", want: "https://[::1]"},
		{name: "non loopback http", value: "http://192.0.2.10:8080", wantErr: true},
		{name: "empty port", value: "http://localhost:", wantErr: true},
		{name: "malformed bracket", value: "http://[::1]foo", wantErr: true},
		{name: "unbracketed ipv6", value: "https://::1", wantErr: true},
		{name: "zone id", value: "https://[fe80::1%25en0]", wantErr: true},
		{name: "userinfo", value: "https://user@example.com", wantErr: true},
		{name: "query separator", value: "https://example.com?", wantErr: true},
		{name: "invalid host", value: "https://foo_bar.example", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveInstallServerURL(test.value, config.Config{}, false)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("canonical origin = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResponseServerURLFailsClosedForMalformedOrigins(t *testing.T) {
	if got := responseServerURL("https://example.com/"); got != "https://example.com" {
		t.Fatalf("unexpected canonical response origin %q", got)
	}
	if got := responseServerURL("https://[::1]foo"); got != "" {
		t.Fatalf("malformed response origin was accepted: %q", got)
	}
}

func TestInstallNodeExplicitEnableVerifiesBeforeReadingOrExchangingJoinCode(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	existing := config.Config{
		ServerURL: server.URL,
		NodeID:    "node-existing",
		NodeToken: "node-token-existing",
	}.WithDefaults()
	if err := config.Save(configPath, existing); err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	statePath := joinExchangeStatePath(configPath)
	state := joinExchangeState{
		Version: 1, ExchangeID: "exchange-pending-0001", ServerURL: server.URL,
		NodeID: "node-existing", CreatedAt: 1,
	}
	if err := saveJoinExchangeState(statePath, state); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	err = installNode([]string{
		"--config", configPath,
		"--server-url", server.URL,
		"--node-id", "node-existing",
		"--join-code-stdin",
		"--exchange-id", state.ExchangeID,
		"--enable-ego-browser",
		"--ego-browser-runtime-root", prepareEgoBrowserMetadataRuntime(t),
	})
	if err == nil || err.Error() != "release_verification_failed" {
		t.Fatalf("unexpected install result: %v", err)
	}
	if requests != 0 {
		t.Fatalf("invalid local release consumed the join exchange with %d request(s)", requests)
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Fatal("failed enable changed the existing config bytes")
	}
	if string(stateAfter) != string(stateBefore) {
		t.Fatal("failed enable changed the pending exchange state")
	}
}

func TestInstallNodeExchangesJoinCodeAndCommitsVerifiedEnable(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("success path requires a root-owned runtime; CI tests it with sudo")
	}
	runtimeRoot := prepareVerifiedEgoBrowserRuntime(t)
	exchangeID := "exchange-install-success-0001"
	joinCode := "join-code-success-0123456789"
	nodeID := "node-install-success"
	version := config.DefaultVersion
	requests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodPost || request.URL.RequestURI() != "/api/v1/node-api/join-code/exchange" {
			t.Errorf("unexpected exchange request %s %s", request.Method, request.URL.RequestURI())
		}
		if request.Header.Get("Authorization") != "" || strings.Contains(request.URL.String(), joinCode) {
			t.Error("join code escaped the request body")
		}
		var payload api.JoinCodeExchangeRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode exchange request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.JoinCode != joinCode || payload.ExchangeID != exchangeID || payload.NodeID != nodeID ||
			payload.EgoBrowserEnabled == nil || !*payload.EgoBrowserEnabled {
			t.Errorf("unexpected exchange payload: %#v", payload)
		}
		writeJoinExchangeResponse(t, response, server.URL, nodeID, exchangeID, version, true)
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	err := installNodeWithStdin(t, joinCode+"\n", []string{
		"--config", configPath,
		"--server-url", server.URL,
		"--node-id", nodeID,
		"--version", version,
		"--join-code-stdin",
		"--exchange-id", exchangeID,
		"--enable-ego-browser",
		"--ego-browser-runtime-root", runtimeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("exchange request count = %d, want 1", requests)
	}
	installed, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.EgoBrowserEnabled || installed.NodeID != nodeID || installed.NodeToken != "node-token-result" {
		t.Fatalf("unexpected installed config: %#v", installed)
	}
	if _, err := os.Lstat(joinExchangeStatePath(configPath)); !os.IsNotExist(err) {
		t.Fatalf("completed exchange state was not removed: %v", err)
	}
}

func TestInstallNodeRecoversLostExchangeResponseWithoutReusingJoinCode(t *testing.T) {
	exchangeID := "exchange-response-lost-0001"
	joinCode := "join-code-response-lost-012345"
	nodeID := "node-response-lost"
	version := config.DefaultVersion
	var payloads []api.JoinCodeExchangeRequest
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload api.JoinCodeExchangeRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode exchange request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		payloads = append(payloads, payload)
		if len(payloads) == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJoinExchangeResponse(t, response, server.URL, nodeID, exchangeID, version, false)
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	firstErr := installNodeWithStdin(t, joinCode+"\n", []string{
		"--config", configPath,
		"--server-url", server.URL,
		"--node-id", nodeID,
		"--version", version,
		"--join-code-stdin",
		"--exchange-id", exchangeID,
	})
	if firstErr == nil {
		t.Fatal("lost response unexpectedly completed installation")
	}
	if _, err := os.Stat(joinExchangeStatePath(configPath)); err != nil {
		t.Fatalf("lost response did not preserve exchange state: %v", err)
	}
	if err := clearJoinExchangeState(joinExchangeStatePath(configPath)); err != nil {
		t.Fatal(err)
	}
	if err := installNode([]string{
		"--config", configPath,
		"--server-url", server.URL,
		"--node-id", nodeID,
		"--version", version,
		"--exchange-id", exchangeID,
	}); err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 || payloads[0].JoinCode != joinCode || payloads[1].JoinCode != "" ||
		payloads[0].ExchangeID != exchangeID || payloads[1].ExchangeID != exchangeID {
		t.Fatalf("exchange was not recovered safely: %#v", payloads)
	}
	installed, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if installed.EgoBrowserEnabled || installed.NodeToken != "node-token-result" {
		t.Fatalf("unexpected recovered config: %#v", installed)
	}
}

func TestJoinExchangeStateIsOwnerOnlyAndRejectsUnsafeLinks(t *testing.T) {
	state := joinExchangeState{
		Version: 1, ExchangeID: "exchange-state-safety-0001", ServerURL: "https://control.example",
		NodeID: "node-state", CreatedAt: 1,
	}
	t.Run("round trip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "exchange.json")
		if err := saveJoinExchangeState(path, state); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode().Perm() != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			t.Fatalf("unsafe exchange state metadata: mode=%#o stat=%#v", info.Mode().Perm(), stat)
		}
		loaded, err := loadJoinExchangeState(path)
		if err != nil || loaded == nil || *loaded != state {
			t.Fatalf("unexpected exchange state round trip: %#v, %v", loaded, err)
		}
		if err := clearJoinExchangeState(path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("exchange state was not cleared: %v", err)
		}
	})
	t.Run("parent symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := saveJoinExchangeState(filepath.Join(link, "exchange.json"), state); err == nil {
			t.Fatal("exchange state accepted a symlink parent")
		}
		if _, err := os.Lstat(filepath.Join(target, "exchange.json")); !os.IsNotExist(err) {
			t.Fatalf("state was written through a parent symlink: %v", err)
		}
	})
	t.Run("hard link", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "exchange.json")
		if err := saveJoinExchangeState(path, state); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, filepath.Join(directory, "alias.json")); err != nil {
			t.Fatal(err)
		}
		if loaded, err := loadJoinExchangeState(path); err == nil || loaded != nil {
			t.Fatalf("multiply linked state was accepted: %#v, %v", loaded, err)
		}
		if err := clearJoinExchangeState(path); err == nil {
			t.Fatal("multiply linked state was removed")
		}
	})
	t.Run("file symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.json")
		if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "exchange.json")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if loaded, err := loadJoinExchangeState(path); err == nil || loaded != nil {
			t.Fatalf("symlink state was accepted: %#v, %v", loaded, err)
		}
	})
}

func TestInstallSSHPreservesExistingAuthorizedKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	keysPath := filepath.Join(dir, "authorized_keys")
	want := []byte("# existing managed keys\n")
	if err := os.WriteFile(keysPath, want, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ServerURL:             "https://example.test",
		NodeID:                "node_1",
		NodeToken:             "node_token",
		SSHAuthorizedKeysPath: keysPath,
	}.WithDefaults()
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := installSSH([]string{"--config", configPath}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(keysPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("authorized keys changed during install: %q", got)
	}
}

func TestConfigureEgoBrowserSynchronizesVersionWithoutEnabling(t *testing.T) {
	root := prepareVerifiedEgoBrowserRuntime(t)
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled_%t", enabled), func(t *testing.T) {
			if enabled && runtime.GOOS == "linux" && os.Geteuid() != 0 {
				t.Skip("success path requires a root-owned runtime; CI tests it with sudo")
			}
			configPath := filepath.Join(t.TempDir(), "config.json")
			cfg := config.Config{
				ServerURL: "https://control.example", NodeID: "node_1",
				EgoBrowserEnabled: enabled, EgoBrowserWrapperVersion: "0.1.0",
				EgoBrowserSkillVersion: "1.2.3", EgoBrowserSkillTreeSHA256: strings.Repeat("a", 64),
			}.WithDefaults()
			if err := config.SaveForUpgrade(configPath, cfg); err != nil {
				t.Fatal(err)
			}
			if err := configureEgoBrowser([]string{"--config", configPath, "--runtime-root", root}); err != nil {
				t.Fatal(err)
			}
			updated, err := config.Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if updated.EgoBrowserEnabled != enabled ||
				updated.EgoBrowserWrapperVersion != egobrowserartifact.PinnedWrapperVersion ||
				updated.EgoBrowserSkillVersion != egobrowserartifact.OfficialSkillVersion ||
				updated.EgoBrowserSkillTreeSHA256 != egobrowserartifact.OfficialSkillTreeSHA256 ||
				updated.EgoBrowserWrapperPath != filepath.Join(root, "current", "bin", "ego-browser") {
				t.Fatalf("ego-browser config was not synchronized safely: %#v", updated)
			}
		})
	}
}

func TestVerifiedEgoBrowserRuntimeRejectsNonRootOwner(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("Linux unprivileged ownership check")
	}
	root := prepareVerifiedEgoBrowserRuntime(t)
	err := egobrowserartifact.VerifyPinned(egobrowserartifact.RuntimeConfig{
		WrapperPath:     filepath.Join(root, "current", "bin", "ego-browser"),
		WrapperVersion:  egobrowserartifact.PinnedWrapperVersion,
		SkillPath:       filepath.Join(root, "current", "skill", "ego-browser"),
		SkillVersion:    egobrowserartifact.OfficialSkillVersion,
		SkillTreeSHA256: egobrowserartifact.OfficialSkillTreeSHA256,
	})
	if err == nil || !strings.Contains(err.Error(), "not root-owned") {
		t.Fatalf("unowned runtime was not rejected: %v", err)
	}
}

func TestConfigureEgoBrowserEnableRejectsUnverifiedRuntime(t *testing.T) {
	root := prepareEgoBrowserMetadataRuntime(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, config.Config{ServerURL: "https://control.example", NodeID: "node_1"}); err != nil {
		t.Fatal(err)
	}
	if err := configureEgoBrowser([]string{"--config", configPath, "--runtime-root", root, "--enable"}); err == nil {
		t.Fatal("unverified ego-browser runtime was enabled")
	}
}

func TestConfigureEgoBrowserRejectsUnverifiedUpgradeWithoutChangingConfig(t *testing.T) {
	root := prepareEgoBrowserMetadataRuntime(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := (config.Config{
		ServerURL: "https://control.example", NodeID: "node_1",
		EgoBrowserEnabled: true, EgoBrowserWrapperVersion: "0.1.12",
		EgoBrowserSkillVersion: "1.2.3", EgoBrowserSkillTreeSHA256: strings.Repeat("a", 64),
	}).WithDefaults()
	if err := config.SaveForUpgrade(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := configureEgoBrowser([]string{"--config", configPath, "--runtime-root", root}); err == nil {
		t.Fatal("unverified runtime was accepted during a browser upgrade")
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed browser upgrade changed the saved config")
	}
}

func TestConfigureEgoBrowserMigratesStaleEnabledConfig(t *testing.T) {
	root := prepareEgoBrowserMetadataRuntime(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		ServerURL: "https://control.example", NodeID: "node_1",
		EgoBrowserEnabled: true, EgoBrowserWrapperVersion: "0.1.0",
	}.WithDefaults()
	if err := config.SaveForUpgrade(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := configureEgoBrowser([]string{
		"--config", configPath, "--runtime-root", root, "--disable",
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.EgoBrowserEnabled || updated.EgoBrowserWrapperVersion != egobrowserartifact.PinnedWrapperVersion {
		t.Fatalf("stale enabled ego-browser config was not migrated: %#v", updated)
	}
}

func prepareEgoBrowserMetadataRuntime(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	release := filepath.Join(root, "releases", egobrowserartifact.PinnedWrapperVersion)
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "bin", "ego-browser"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(release, "skill", "ego-browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "skill", "ego-browser", "SKILL.md"), []byte("# skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"VERSION":           egobrowserartifact.PinnedWrapperVersion + "\n",
		"SKILL_VERSION":     egobrowserartifact.OfficialSkillVersion + "\n",
		"SKILL_TREE_SHA256": strings.Repeat("a", 64) + "\n",
	} {
		if err := os.WriteFile(filepath.Join(release, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(release, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return root
}

func prepareVerifiedEgoBrowserRuntime(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	root := t.TempDir()
	release := filepath.Join(root, "releases", egobrowserartifact.PinnedWrapperVersion)
	skill := filepath.Join(release, "skill", "ego-browser")
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(skill, os.DirFS(filepath.Join(repositoryRoot, "internal", "managedskills", "skills", "ego-browser"))); err != nil {
		t.Fatal(err)
	}
	wrapper := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(filepath.Join(release, "bin", "ego-browser"), wrapper, 0o555); err != nil {
		t.Fatal(err)
	}
	sourceManifest, err := os.ReadFile(filepath.Join(repositoryRoot, "ego-browser-skill-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "ego-browser-skill-source.json"), sourceManifest, 0o444); err != nil {
		t.Fatal(err)
	}
	wrapperDigest := sha256.Sum256(wrapper)
	manifestDigest := sha256.Sum256(sourceManifest)
	for name, value := range map[string]string{
		"VERSION":                egobrowserartifact.PinnedWrapperVersion,
		"WRAPPER_SHA256":         hex.EncodeToString(wrapperDigest[:]),
		"SKILL_VERSION":          egobrowserartifact.OfficialSkillVersion,
		"SKILL_TREE_SHA256":      egobrowserartifact.OfficialSkillTreeSHA256,
		"SOURCE_MANIFEST_SHA256": hex.EncodeToString(manifestDigest[:]),
	} {
		if err := os.WriteFile(filepath.Join(release, name), []byte(value+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(release, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return root
}

func installNodeWithStdin(t *testing.T, input string, args []string) error {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = previous
		_ = reader.Close()
	}()
	return installNode(args)
}

func writeJoinExchangeResponse(
	t *testing.T,
	response http.ResponseWriter,
	serverURL string,
	nodeID string,
	exchangeID string,
	version string,
	enabled bool,
) {
	t.Helper()
	intent := "false"
	if enabled {
		intent = "true"
	}
	material := strings.Join([]string{
		"node-join-profile-v1",
		serverURL,
		nodeID,
		"node-default",
		egobrowserartifact.PinnedWrapperVersion,
		egobrowserartifact.OfficialSkillVersion,
		version,
		"sha256:" + egobrowserartifact.OfficialSkillTreeSHA256,
		intent,
	}, "\x00")
	profileDigest := sha256.Sum256([]byte(material))
	if err := json.NewEncoder(response).Encode(map[string]any{
		"data": map[string]any{
			"node_id":                    nodeID,
			"node_token":                 "node-token-result",
			"ego_browser_enabled":        enabled,
			"ego_browser_enabled_intent": enabled,
			"exchange_id":                exchangeID,
			"server_origin":              serverURL,
			"release_profile":            "node-default",
			"wrapper_version":            egobrowserartifact.PinnedWrapperVersion,
			"skill_version":              egobrowserartifact.OfficialSkillVersion,
			"runtime_version":            version,
			"artifact_digest":            "sha256:" + egobrowserartifact.OfficialSkillTreeSHA256,
			"profile_digest":             hex.EncodeToString(profileDigest[:]),
		},
	}); err != nil {
		t.Errorf("write exchange response: %v", err)
	}
}
