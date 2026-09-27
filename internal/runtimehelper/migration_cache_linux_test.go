package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestMigrationDispatchNeverUsesGenericCacheAsOriginalAuthority(t *testing.T) {
	for _, kind := range []string{"cache-only", "exact", "different-input", "missing-copy", "pending"} {
		t.Run(kind, func(t *testing.T) {
			engine, binding, _ := accountTakeoverFixture(t)
			task := "migrate_tool_account_runtime:" + binding.AccountID + ":" + binding.TaskID
			account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
			backup := filepath.Join(engine.config.StateRoot, "migrations", shortDigest(task, 32))
			copy := skillmanager.AccountCopyReceipt{Version: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, TaskID: task, InputDigest: migrationDigest([]string{"docker_sandbox", "native", account, backup}), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
			receipt := skillmanager.AccountMigrationReceipt{Version: 1, Copy: copy, InputDigest: migrationDigest([]string{copy.InputDigest, engine.config.NodeUser, engine.config.SetfaclPath, engine.config.SystemdRunPath, engine.config.SystemctlPath, engine.config.CgroupRoot}), State: "started"}
			store, err := engine.openSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if kind == "cache-only" {
				if err := os.MkdirAll(account, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(account, "learning"), []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "cache-only" {
				if err := skillmanager.BeginAccountCopy(store, copy); err != nil {
					t.Fatal(err)
				}
				if err := skillmanager.FinishAccountCopy(store, copy, "copied"); err != nil {
					t.Fatal(err)
				}
				if err := skillmanager.BeginAccountMigration(store, receipt); err != nil {
					t.Fatal(err)
				}
				if kind != "pending" {
					if err := skillmanager.FinishAccountMigration(store, receipt, "succeeded"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "missing-copy" {
				files, err := filepath.Glob(filepath.Join(engine.config.SkillStateRoot, "account-copy-*.json"))
				if err != nil || len(files) != 1 {
					t.Fatal("copy fixture missing", err)
				}
				if err := os.Remove(files[0]); err != nil {
					t.Fatal(err)
				}
			}
			if err := engine.saveResult(task, map[string]any{"migrated": true, "runtime_backend": "native", "tool_account_id": binding.AccountID, "cache_only_marker": true}); err != nil {
				t.Fatal(err)
			}
			request := Request{Version: ProtocolVersion, RequestID: task, Operation: "migrate_account", Payload: map[string]any{"tool_account_id": binding.AccountID, "user_id": binding.UserID, "tool_type": "claude", "source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native"}}
			if kind == "different-input" {
				request.Payload["source_runtime_backend"], request.Payload["target_runtime_backend"] = "native", "docker_sandbox"
			}
			result, err := NewEngine(engine.config).Execute(context.Background(), request)
			if kind == "exact" {
				if err != nil || result["migrated"] != true || result["cache_only_marker"] != nil {
					t.Fatal("exact typed replay did not revalidate original evidence", err)
				}
			} else if err == nil {
				t.Fatal("generic cached result bypassed migration authority")
			}
			for _, path := range []string{account, backup} {
				if path == account && kind == "cache-only" {
					data, err := os.ReadFile(filepath.Join(account, "learning"))
					if err != nil || string(data) != "retained" {
						t.Fatal("cached source changed", err)
					}
					continue
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cached history caused new side effects", err)
				}
			}
		})
	}
}
