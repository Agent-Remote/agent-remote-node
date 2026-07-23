package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
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
	applySystemInstallPaths(&cfg, "/opt/agent", "/srv/node", "/srv/data", "/opt/claude")
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
}
