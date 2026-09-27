package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func explicitSourceRepairRequest(original Request, node string) Request {
	request := explicitRecoveryRequest(original, node)
	binding := request.Payload["binding"].(map[string]any)
	binding["version"], binding["action"] = 3, "repair_source"
	return request
}

func TestMigrationRepairPreservesInterruptedOriginalAndRevalidatesCompletion(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root permission restoration")
	}
	for _, kind := range []string{"repair", "repair-previous-boot"} {
		for _, fault := range []string{"none", "cancel-during-write", "missing-backup", "backup-permissions", "invalid-account", "parent-permissions", "foreign-unit", "empty-cgroup", "populated-cgroup", "linked-cgroup", "cancelled"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				engine, originalRequest, original, root, backup := completedMigrationFixture(t, kind)
				store, err := engine.openSkillStateRoot()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				account := filepath.Join(engine.config.AccountRoot, original.Copy.UserID, "tool-accounts", "claude", original.Copy.AccountID)
				if err := os.Chmod(filepath.Join(account, "original"), 0644); err != nil {
					t.Fatal(err)
				}
				preparePreviousBootFailure(t, engine, store, original, fault, root, account, backup)
				if fault == "backup-permissions" || fault == "parent-permissions" {
					path := filepath.Join(backup, "original")
					if fault == "parent-permissions" {
						path = filepath.Dir(account)
					}
					if err := os.Chmod(path, 0755); err != nil {
						t.Fatal(err)
					}
				}
				request := explicitSourceRepairRequest(originalRequest, engine.config.NodeID)
				metadata := migrationRecoveryInventory(t, engine.config.SkillStateRoot)
				before := migrationRecoveryInventory(t, account, backup)
				base, cancel := context.WithCancel(context.Background())
				defer cancel()
				var ctx context.Context = base
				if fault == "cancelled" {
					cancel()
				} else if fault == "cancel-during-write" {
					ctx = migrationRestoreCancellation{Context: base, cancel: cancel, path: filepath.Join(account, "original")}
				}
				var result map[string]any
				if fault == "cancel-during-write" {
					// Exercise the synchronous executor directly so timeout wrapping does not
					// hide the deterministic context observing the first actual chmod.
					var authorization accountmigration.Authorization
					if err := decodeStrictPayload(request.Payload, &authorization); err != nil {
						t.Fatal(err)
					}
					intent, prepareErr := skillmanager.PrepareAccountMigrationRepair(store, original, authorization.Binding.OriginalTaskRecordID, "docker_sandbox", "native")
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					phases, phaseErr := engine.inspectPassiveMigrationPhases(ctx, store, original.Copy, currentBootID(), false)
					if phaseErr != nil {
						t.Fatal(phaseErr)
					}
					err = engine.restoreInterruptedMigration(ctx, store, intent, authorization, phases, currentBootID(), false, account, backup)
				} else {
					result, err = NewEngine(engine.config).Execute(ctx, request)
				}
				allowed := fault == "none" || kind == "repair" && fault == "empty-cgroup"
				if fault == "cancel-during-write" {
					if err == nil || !errors.Is(base.Err(), context.Canceled) {
						t.Fatal("actual interrupted permission write did not stay pending", err)
					}
					if _, err := skillmanager.ReadAccountMigrationRepairCompletion(store, original.Copy.TaskID); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("cancelled repair published completion", err)
					}
					if skillmanager.CheckAccountMigrationHistory(store, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID) == nil {
						t.Fatal("unfinished repair opened history")
					}
					request.Payload["lease_attempt"] = 2
					result, err = NewEngine(engine.config).Execute(context.Background(), request)
					allowed = true
				}
				if allowed {
					if err != nil || result["recovered"] != true {
						t.Fatal("verified original repair rejected", err)
					}
					if _, err := engine.inspectMigrationCompletion(context.Background(), store, original, account, backup, "failed"); err != nil {
						t.Fatal("source was not exactly restored", err)
					}
					completion, err := skillmanager.ReadAccountMigrationRepairCompletion(store, original.Copy.TaskID)
					if err != nil || completion.Attempt.LeaseAttempt != int64(request.Payload["lease_attempt"].(int)) || completion.Attempt.BootID != currentBootID() {
						t.Fatal("repair lost current delivery identity", err)
					}
					snapshot := migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)
					request.Payload["lease_attempt"] = 3
					if _, err := NewEngine(engine.config).Execute(context.Background(), request); err != nil {
						t.Fatal("lost completion reply did not revalidate", err)
					}
					if !reflect.DeepEqual(snapshot, migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)) {
						t.Fatal("completed retry mutated files")
					}
					if err := os.Chmod(filepath.Join(account, "original"), 0644); err != nil {
						t.Fatal(err)
					}
					if _, err := NewEngine(engine.config).Execute(context.Background(), request); err == nil {
						t.Fatal("completion substituted for current permissions")
					}
					info, err := os.Stat(filepath.Join(account, "original"))
					if err != nil || info.Mode().Perm() != 0644 {
						t.Fatal("completed repair resumed permission writes", err)
					}
				} else {
					if err == nil {
						t.Fatal("unsafe repair accepted")
					}
					if !reflect.DeepEqual(before, migrationRecoveryInventory(t, account, backup)) {
						t.Fatal("rejected preflight changed account or backup")
					}
				}
				after := migrationRecoveryInventory(t, engine.config.SkillStateRoot)
				for path, value := range metadata {
					if value.Mode.IsRegular() && !reflect.DeepEqual(value, after[path]) {
						t.Fatal("repair changed original metadata", path)
					}
				}
				calls, _ := os.ReadFile(filepath.Join(root, "calls"))
				for _, call := range strings.Fields(string(calls)) {
					if call != "show" {
						t.Fatal("repair launched a subprocess writer", call)
					}
				}
			})
		}
	}
}
