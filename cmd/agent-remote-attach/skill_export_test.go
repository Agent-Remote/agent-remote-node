package main

import (
	"encoding/json"
	"errors"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenExportCommandRequiresExactBoundedSnapshotProtocol(t *testing.T) {
	id := "55555555-5555-4555-8555-555555555555"
	command := "agent-remote-skill-export --snapshot " + id + " --protocol 1"
	got, err := frozenExportFromCommand(command)
	if err != nil || got != id {
		t.Fatal("canonical frozen export rejected", err)
	}
	for _, value := range []string{
		command + " --path /etc/passwd", command + ";id", "agent-remote-skill-export",
		"agent-remote-skill-export --protocol 1 --snapshot " + id,
		"agent-remote-skill-export --snapshot ../../etc --protocol 1",
		"agent-remote-skill-export --snapshot " + id + " --protocol 2",
		"agent-remote-skill-export --snapshot '" + id + "' --protocol 1",
		"agent-remote-skill-export-other --snapshot " + id + " --protocol 1",
	} {
		if _, err := frozenExportFromCommand(value); err == nil {
			t.Fatal("unsafe command accepted", value)
		}
	}
}

func TestFrozenExportDispatchHasNoSyncFallbackOrCallerSuppliedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{ServerURL: "https://control.example.test", NodeID: "33333333-3333-4333-8333-333333333333", NodeToken: "disposable-test", AllowedRuntimeBackends: []string{"native"}}.WithDefaults()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	valid := "agent-remote-skill-export --snapshot 55555555-5555-4555-8555-555555555555 --protocol 1"
	args := []string{"--config", path, "--device", "77777777-7777-4777-8777-777777777777", "--ssh-key", "88888888-8888-4888-8888-888888888888", "--dry-run"}
	t.Setenv("SSH_ORIGINAL_COMMAND", valid)
	if err := run(args); err != nil {
		t.Fatal("exact dispatch failed", err)
	}
	for _, command := range []string{valid + " --ssh-key 88888888-8888-4888-8888-888888888888", "agent-remote-skill-export --path /etc/passwd", "agent-remote-skill-export-other"} {
		t.Setenv("SSH_ORIGINAL_COMMAND", command)
		if err := run(args); !errors.Is(err, skillexport.ErrUnavailable) {
			t.Fatal("invalid export reached fallback", err)
		}
	}
	t.Setenv("SSH_ORIGINAL_COMMAND", valid)
	if err := run([]string{"--config", path, "--device", "77777777-7777-4777-8777-777777777777", "--dry-run"}); !errors.Is(err, skillexport.ErrUnavailable) {
		t.Fatal("missing forced key accepted", err)
	}
}
