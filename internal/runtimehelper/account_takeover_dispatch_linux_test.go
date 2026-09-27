package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperTakeoverCaptureDispatchRetainsOriginalAcrossRestart(t *testing.T) {
	engine, binding, _ := accountTakeoverFixture(t)
	binding.DirectoryEpoch = 9007199254740993
	source := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID, ".claude", "skills")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "learned"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	capture, err := client.CaptureAccountTakeover(context.Background(), "capture", binding, nil)
	if err != nil || capture.Binding != binding || !capture.SourceExists {
		t.Fatal(capture, err)
	}
	if err := os.WriteFile(filepath.Join(source, "learned"), []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	engine.config.SystemctlPath = "/unavailable/systemctl"
	restarted, _ := serveCaptureTest(t, NewEngine(engine.config), os.Getuid())
	replay, err := restarted.CaptureAccountTakeover(context.Background(), "capture-retry", binding, nil)
	if err != nil || replay != capture {
		t.Fatal("capture was repeated", replay, err)
	}
	actual, manifest, err := restarted.ReadAccountCapture(context.Background(), "read", binding)
	if err != nil || actual != capture || len(manifest.Entries) != 1 {
		t.Fatal(actual, manifest, err)
	}
	binding.DirectoryEpoch++
	if _, err := restarted.CaptureAccountTakeover(context.Background(), "changed", binding, nil); err == nil {
		t.Fatal("changed reservation accepted")
	}
	data, err := os.ReadFile(filepath.Join(source, "learned"))
	if err != nil || string(data) != "later" {
		t.Fatal("source changed", err)
	}
}

func TestHelperTakeoverCaptureDispatchWaitsWithoutStoppingWriters(t *testing.T) {
	engine, binding, root := accountTakeoverFixture(t)
	backend := "native"
	inventory := []skillmanager.AccountWriter{{Kind: "session", NodeID: binding.NodeID, ResourceID: binding.TaskID, RuntimeBackend: &backend}}
	binding.InventoryDigest, _ = skillmanager.AccountInventoryDigest(inventory)
	if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte(unitFields("active", "", "success", "0", "0")), 0600); err != nil {
		t.Fatal(err)
	}
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	_, err := client.CaptureAccountTakeover(context.Background(), "capture", binding, inventory)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "MIGRATION_PENDING" {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || strings.Contains(string(calls), "stop") || strings.Contains(string(calls), "kill") {
		t.Fatal("writer was stopped", err)
	}
	if err := engine.requireLegacyAccountRuntime(binding.UserID, binding.AccountID); !errors.Is(err, errAccountMigrationPending) {
		t.Fatal("fence absent", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte("LoadState=not-found\nActiveState=inactive\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CaptureAccountTakeover(context.Background(), "capture-retry", binding, inventory); err != nil {
		t.Fatal(err)
	}
}

func TestHelperTakeoverCaptureRejectsUntrustedInputBeforeFence(t *testing.T) {
	for _, change := range []string{"backend", "node", "inventory", "path", "alias", "cached"} {
		t.Run(change, func(t *testing.T) {
			engine, binding, _ := accountTakeoverFixture(t)
			payload, err := Map(accountTakeoverRequest{binding, []skillmanager.AccountWriter{}})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "backend":
				payload["binding"].(map[string]any)["runtime_backend"] = "docker_sandbox"
			case "node":
				payload["binding"].(map[string]any)["node_id"] = binding.UserID
			case "inventory":
				payload["inventory"] = []skillmanager.AccountWriter{{Kind: "session", NodeID: binding.NodeID, ResourceID: binding.TaskID}}
			case "path":
				payload["path"] = "/other"
			case "alias":
				payload["Binding"] = payload["binding"]
				delete(payload, "binding")
			case "cached":
				if err := engine.saveResult("capture", map[string]any{"status": "succeeded"}); err != nil {
					t.Fatal(err)
				}
				payload["binding"].(map[string]any)["directory_epoch"] = 0
			}
			if _, err := engine.Execute(context.Background(), Request{Version: 1, RequestID: "capture", Operation: accountTakeoverOperation, Payload: payload}); err == nil {
				t.Fatal("untrusted input accepted")
			}
			if _, err := os.Lstat(engine.config.SkillStateRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid capture created state", err)
			}
		})
	}
}

func TestHelperTakeoverCaptureAcceptsMaximumInventory(t *testing.T) {
	engine, binding, _ := accountTakeoverFixture(t)
	inventory := make([]skillmanager.AccountWriter, 10000)
	for index := range inventory {
		inventory[index] = skillmanager.AccountWriter{Kind: "import", NodeID: binding.NodeID, ResourceID: binding.TaskID, TaskID: &binding.TaskID}
	}
	binding.InventoryDigest, _ = skillmanager.AccountInventoryDigest(inventory)
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	if _, err := client.CaptureAccountTakeover(context.Background(), "capture", binding, inventory); err != nil {
		t.Fatal("bounded inventory rejected", err)
	}
}
