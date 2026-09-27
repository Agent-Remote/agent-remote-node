package runtimehelper

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Fixture authority is sealed before the simulated copy, including the original boot.
func seedMigrationSourceBaseline(t *testing.T, engine Engine, store *os.Root, original skillmanager.AccountMigrationReceipt, account string) {
	t.Helper()
	inventory, err := scanMigrationInventory(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	parents, err := engine.migrationParentPermissions(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	err = skillmanager.BeginAccountMigrationBaseline(store, skillmanager.AccountMigrationBaseline{
		Version: 1, Migration: original,
		Account:       skillmanager.MigrationObjectIdentity{DeviceMajor: inventory.root.deviceMajor, DeviceMinor: inventory.root.deviceMinor, Inode: inventory.root.inode},
		ContentDigest: hex.EncodeToString(inventory.content[:]), PermissionsDigest: hex.EncodeToString(inventory.permissions[:]), Parents: parents,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSourceVerificationRequiresImmutableExactRollback(t *testing.T) {
	for _, boot := range []string{"attested-rollback", "attested-rollback-previous-boot"} {
		for _, fault := range []string{"none", "started", "missing-baseline", "missing-attestation", "missing-phase", "missing-backup", "incomplete-backup", "invalid-account", "source-permissions", "backup-permissions", "parent-permissions", "foreign-unit", "empty-cgroup", "populated-cgroup", "linked-cgroup", "cancelled"} {
			t.Run(boot+"/"+fault, func(t *testing.T) {
				engine, request, original, root, backup := completedMigrationFixture(t, boot)
				store, err := engine.openSkillStateRoot()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				account := filepath.Join(engine.config.AccountRoot, original.Copy.UserID, "tool-accounts", "claude", original.Copy.AccountID)
				if fault != "started" {
					if err := skillmanager.AttestAccountMigration(store, original, "failed"); err != nil {
						t.Fatal(err)
					}
					if err := skillmanager.FinishAccountMigration(store, original, "failed"); err != nil {
						t.Fatal(err)
					}
				}
				preparePreviousBootFailure(t, engine, store, original, fault, root, account, backup)
				switch fault {
				case "missing-baseline", "missing-attestation":
					prefix := "migration-baseline-"
					if fault == "missing-attestation" {
						prefix = "migration-attestation-"
					}
					entries, err := os.ReadDir(engine.config.SkillStateRoot)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), prefix) {
							if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, entry.Name())); err != nil {
								t.Fatal(err)
							}
							found = true
						}
					}
					if !found {
						t.Fatal("fixture evidence missing")
					}
				case "source-permissions", "backup-permissions", "parent-permissions":
					path := filepath.Join(account, "original")
					if fault == "backup-permissions" {
						path = filepath.Join(backup, "original")
					}
					if fault == "parent-permissions" {
						path = filepath.Dir(account)
					}
					if err := os.Chmod(path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				recovery := explicitRecoveryRequest(request, engine.config.NodeID)
				recovery.Payload["binding"].(map[string]any)["version"] = 2
				recovery.Payload["binding"].(map[string]any)["action"] = "verify_source"
				before := migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if fault == "cancelled" {
					cancel()
				}
				result, err := NewEngine(engine.config).Execute(ctx, recovery)
				allowed := fault == "none" || (boot == "attested-rollback" && fault == "empty-cgroup")
				if allowed {
					if err != nil || result["recovered"] != true {
						t.Fatal("exact restored source rejected", err)
					}
				} else if err == nil {
					t.Fatal("incomplete source evidence accepted")
				}
				if !reflect.DeepEqual(before, migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)) {
					t.Fatal("verification mutated original state")
				}
				calls, _ := os.ReadFile(filepath.Join(root, "calls"))
				for _, call := range strings.Fields(string(calls)) {
					if call != "show" {
						t.Fatal("verification invoked mutation", call)
					}
				}
			})
		}
	}
}
