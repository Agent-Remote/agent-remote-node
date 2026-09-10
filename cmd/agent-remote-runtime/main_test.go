package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func TestApplyBrowserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	nodeConfig := config.Config{
		ServerURL:              "https://control.example.test",
		NodeID:                 "node-test",
		AllowedRuntimeBackends: []string{"native"},
		DockerBinaryPath:       "/usr/bin/docker",
		BrowserRoot:            "/srv/browser-sessions",
		BrowserImage:           "kasmweb/chrome:test",
		BrowserPublicBaseURL:   "https://browser.example.test",
		BrowserDockerNetwork:   "agent-remote_default",
	}.WithDefaults()
	if err := config.Save(path, nodeConfig); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := runtimehelper.EngineConfig{}
	if err := applyBrowserConfig(&runtimeConfig, path); err != nil {
		t.Fatal(err)
	}
	if runtimeConfig.DockerBinaryPath != nodeConfig.DockerBinaryPath ||
		runtimeConfig.BrowserRoot != nodeConfig.BrowserRoot ||
		runtimeConfig.BrowserImage != nodeConfig.BrowserImage ||
		runtimeConfig.BrowserPublicBaseURL != nodeConfig.BrowserPublicBaseURL ||
		runtimeConfig.BrowserDockerNetwork != nodeConfig.BrowserDockerNetwork {
		t.Fatalf("browser runtime config was not applied: %#v", runtimeConfig)
	}
}

func TestExecuteSpecDoesNotReadNodeConfig(t *testing.T) {
	want := errors.New("action reached")
	called := false
	err := executeSpec([]string{
		"--spec", filepath.Join(t.TempDir(), "spec.json"),
		"--node-config", filepath.Join(t.TempDir(), "unreadable-config.json"),
	}, func(runtimeConfig runtimehelper.EngineConfig, _ string) error {
		called = true
		if runtimeConfig.NodeConfigPath != "" {
			t.Fatalf("session child retained a node config path: %q", runtimeConfig.NodeConfigPath)
		}
		return want
	})
	if !called || !errors.Is(err, want) {
		t.Fatalf("executeSpec did not reach the action without opening node config: called=%t err=%v", called, err)
	}
}
