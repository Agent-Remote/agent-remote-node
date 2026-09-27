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

func TestAccountMigrationEvidenceGatesReplayAndNativeTakeover(t *testing.T) {
	for _, kind := range []string{"succeeded", "failed", "started", "missing", "copy-only", "missing-copy", "corrupt", "different-input"} {
		t.Run(kind, func(t *testing.T) {
			engine, binding, root := accountTakeoverFixture(t)
			task := "migrate_tool_account_runtime:" + binding.AccountID + ":" + binding.TaskID
			account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
			backup := filepath.Join(root, "original-backup")
			copy := skillmanager.AccountCopyReceipt{Version: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, TaskID: task, InputDigest: migrationDigest([]string{"docker_sandbox", "native", account, backup}), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
			record := skillmanager.AccountMigrationReceipt{Version: 1, Copy: copy, InputDigest: migrationDigest([]string{copy.InputDigest, engine.config.NodeUser, engine.config.SetfaclPath, engine.config.SystemdRunPath, engine.config.SystemctlPath, engine.config.CgroupRoot}), State: "started"}
			store, err := engine.openSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if kind != "missing" {
				if err := skillmanager.BeginAccountCopy(store, copy); err != nil {
					t.Fatal(err)
				}
				if err := skillmanager.FinishAccountCopy(store, copy, "copied"); err != nil {
					t.Fatal(err)
				}
				if kind != "copy-only" {
					if err := skillmanager.BeginAccountMigration(store, record); err != nil {
						t.Fatal(err)
					}
					if kind != "started" {
						outcome := "succeeded"
						if kind == "failed" {
							outcome = "failed"
						}
						if err := skillmanager.FinishAccountMigration(store, record, outcome); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if kind == "missing-copy" || kind == "corrupt" {
				entries, err := os.ReadDir(engine.config.SkillStateRoot)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					name := entry.Name()
					if kind == "missing-copy" && strings.HasPrefix(name, "account-copy-") {
						err = store.Remove(name)
					}
					if kind == "corrupt" && strings.HasPrefix(name, "migration-account-copy-") {
						err = os.WriteFile(filepath.Join(engine.config.SkillStateRoot, name), []byte("{"), 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := skillmanager.CloseAccountImports(store, skillmanager.AccountFence{Version: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, DirectoryEpoch: 1}); err != nil {
				t.Fatal(err)
			}
			if kind == "different-input" {
				backup += "-replacement"
			}
			err = engine.executeAccountMigration(context.Background(), task, binding.UserID, binding.AccountID, "docker_sandbox", "native", account, backup)
			switch kind {
			case "succeeded":
				if err != nil {
					t.Fatal("terminal replay was blocked by fence", err)
				}
			case "failed":
				if !errors.Is(err, errMigrationFailed) {
					t.Fatal("failed replay lost its result", err)
				}
			default:
				if err == nil {
					t.Fatal("unverified replay succeeded")
				}
			}
			// Replay must not even create a backup or a missing account, including after fencing.
			for _, path := range []string{account, backup} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("replay mutated account state", err)
				}
			}
			writers := []skillmanager.AccountWriter{{Kind: "backend", NodeID: binding.NodeID, ResourceID: task, TaskID: &binding.TaskID}}
			binding.InventoryDigest, err = skillmanager.AccountInventoryDigest(writers)
			if err != nil {
				t.Fatal(err)
			}
			_, err = engine.captureNativeAccountTakeover(context.Background(), binding, writers)
			complete := kind == "succeeded" || kind == "failed" || kind == "different-input"
			if complete && err != nil || !complete && !errors.Is(err, errTakeoverWritersUnknown) {
				t.Fatal("takeover did not require complete writer proof", err)
			}
		})
	}
}
