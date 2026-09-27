package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func explicitRecoveryRequest(original Request, node string) Request {
	id := "recover_tool_account_runtime:" + original.Payload["tool_account_id"].(string) + ":aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	binding := map[string]any{
		"version": 1, "task_id": id, "task_record_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"original_task_id": original.RequestID, "original_task_record_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"node_id": node,
	}
	for key, value := range original.Payload {
		binding[key] = value
	}
	return Request{Version: ProtocolVersion, RequestID: id, Operation: "recover_account_migration", Payload: map[string]any{"binding": binding, "lease_attempt": 1}}
}

func TestMigrationExplicitRecoveryOnlyUsesOriginalEvidence(t *testing.T) {
	for _, kind := range []string{"target", "already-succeeded", "missing-original", "wrong-original", "legacy", "pre-ownership-evidence", "previous-boot", "rollback", "rollback-intent-only", "missing-target", "invalid-account", "missing-backup", "foreign-unit", "populated", "cached-success", "managed-fence"} {
		t.Run(kind, func(t *testing.T) {
			engine, original, receipt, root, backup := completedMigrationFixture(t, kind)
			request := explicitRecoveryRequest(original, engine.config.NodeID)
			if kind == "already-succeeded" {
				if _, err := engine.Execute(context.Background(), original); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "missing-original" || kind == "cached-success" {
				entries, err := os.ReadDir(engine.config.SkillStateRoot)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "migration-account-copy-") {
						if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, entry.Name())); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if kind == "wrong-original" {
				request.Payload["binding"].(map[string]any)["original_task_id"] = "migrate_tool_account_runtime:" + receipt.Copy.AccountID + ":dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			}
			if kind == "cached-success" {
				if err := engine.saveResult(request.RequestID, map[string]any{"recovered": true}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "managed-fence" {
				store, err := engine.openSkillStateRoot()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				_, err = skillmanager.CloseAccountImports(store, skillmanager.AccountFence{Version: 1, NodeID: receipt.Copy.NodeID, UserID: receipt.Copy.UserID, AccountID: receipt.Copy.AccountID, DirectoryEpoch: 1})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(backup, "original"))
			if kind != "missing-backup" && err != nil {
				t.Fatal(err)
			}
			result, err := NewEngine(engine.config).Execute(context.Background(), request)
			if kind == "target" || kind == "already-succeeded" {
				if err != nil || result["recovered"] != true {
					t.Fatalf("completed recovery rejected: %v", err)
				}
				want, _ := json.Marshal(request.Payload)
				got, _ := json.Marshal(result["authorization"])
				var gotValue, wantValue any
				_ = json.Unmarshal(got, &gotValue)
				_ = json.Unmarshal(want, &wantValue)
				if !reflect.DeepEqual(gotValue, wantValue) {
					t.Fatal("recovery returned a different binding")
				}
			} else if err == nil {
				t.Fatal("incomplete or foreign evidence authorized recovery")
			}
			calls, _ := os.ReadFile(filepath.Join(root, "calls"))
			for _, call := range strings.Split(string(calls), "\n") {
				if call != "" && call != "show" {
					t.Fatal("recovery launched a mutating command", call)
				}
			}
			after, readErr := os.ReadFile(filepath.Join(backup, "original"))
			if kind != "missing-backup" && (readErr != nil || string(after) != string(before)) {
				t.Fatal("original backup changed")
			}
		})
	}
}

func TestMigrationExplicitRecoveryDoesNotCreateMissingStore(t *testing.T) {
	engine, original, _, _, _ := completedMigrationFixture(t, "target")
	if err := os.RemoveAll(engine.config.SkillStateRoot); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Execute(context.Background(), explicitRecoveryRequest(original, engine.config.NodeID))
	if err == nil {
		t.Fatal("missing original store admitted")
	}
	if _, err := os.Lstat(engine.config.SkillStateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery created an empty store")
	}
}
