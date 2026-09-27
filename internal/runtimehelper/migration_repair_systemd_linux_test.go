package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestMigrationRepairSystemdRequiresStoppedOriginalWithoutRewritingIt(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd and root identities")
	}
	for _, source := range []string{"docker_sandbox", "native"} {
		for index, interrupted := range []bool{false, true} {
			t.Run(source+"/"+map[bool]string{false: "completed-target", true: "interrupted-target"}[interrupted], func(t *testing.T) {
				fixture := newMigrationCompletionFixture(t, index+6)
				engine := fixture.engine
				ready, release := filepath.Join(fixture.root, "ready"), filepath.Join(fixture.root, "release")
				if interrupted {
					engine.config.SetfaclPath = delayedFinalMigrationACL(t, fixture.root, ready, release)
				}
				target := "native"
				if source == "native" {
					target = "docker_sandbox"
				}
				fixture.request.Payload["source_runtime_backend"], fixture.request.Payload["target_runtime_backend"] = source, target
				prior := fixture.original.Copy
				original := engine.accountMigrationReceipt(prior.TaskID, prior.UserID, prior.AccountID, source, target, fixture.account, fixture.backup)
				copy := original.Copy
				identity, err := engine.dockerRuntimeIdentity()
				if source == "native" {
					identity, err = engine.ensureIdentity(copy.UserID)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := filepath.WalkDir(fixture.account, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					return os.Lchown(path, identity.UID, identity.GID)
				}); err != nil {
					t.Fatal(err)
				}
				store, err := engine.openSkillStateRoot()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				if err := skillmanager.BeginAccountMigration(store, original); err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				if err := engine.captureMigrationBaseline(ctx, store, original, fixture.account); err != nil {
					t.Fatal(err)
				}
				if err := ensureRootDirectory(fixture.backup, 0700); err != nil {
					t.Fatal(err)
				}
				if err := engine.backupMigratingAccount(ctx, copy.TaskID, copy.UserID, copy.AccountID, source, target, fixture.account, fixture.backup); err != nil {
					t.Fatal(err)
				}
				request := explicitSourceRepairRequest(fixture.request, engine.config.NodeID)
				if interrupted {
					work, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					done := make(chan error, 1)
					go func() {
						done <- engine.applyMigrationOwnership(work, copy, "target", copy.UserID, fixture.account, target)
					}()
					for !pathExists(ready) && work.Err() == nil {
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
					if err := <-done; !errors.Is(err, errMigrationWritersUnknown) || !pathExists(ready) {
						t.Fatal("fixture did not interrupt live traversal", err)
					}
					before := migrationRecoveryInventory(t, engine.config.SkillStateRoot, fixture.account, fixture.backup)
					if _, err := NewEngine(engine.config).Execute(ctx, request); err == nil {
						t.Fatal("live original writer admitted source repair")
					}
					if !reflect.DeepEqual(before, migrationRecoveryInventory(t, engine.config.SkillStateRoot, fixture.account, fixture.backup)) {
						t.Fatal("live writer rejection mutated retained state")
					}
					if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
						t.Fatal(err)
					}
					phase, err := skillmanager.ReadMigrationPhase(store, copy, "target-2")
					if err != nil || (phase.State != "starting" && phase.State != "observed") {
						t.Fatal("original pending writer was lost", err)
					}
					deadline := time.Now().Add(5 * time.Second)
					for engine.confirmMigrationPhaseQuiescence(ctx, phase) != nil {
						if time.Now().After(deadline) {
							t.Fatal("released original did not stop")
						}
						time.Sleep(10 * time.Millisecond)
					}
				} else if err := engine.applyMigrationOwnership(ctx, copy, "target", copy.UserID, fixture.account, target); err != nil {
					t.Fatal(err)
				}
				metadata := migrationRecoveryInventory(t, engine.config.SkillStateRoot)
				backup := migrationRecoveryInventory(t, fixture.backup)
				launches, err := os.ReadFile(fixture.launches)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := NewEngine(engine.config).Execute(ctx, request); err != nil {
					t.Fatal("stopped original could not repair", err)
				}
				if _, err := engine.inspectMigrationCompletion(ctx, store, original, fixture.account, fixture.backup, "failed"); err != nil {
					t.Fatal("repair lost exact source permissions", err)
				}
				if !reflect.DeepEqual(backup, migrationRecoveryInventory(t, fixture.backup)) {
					t.Fatal("repair changed original backup")
				}
				after := migrationRecoveryInventory(t, engine.config.SkillStateRoot)
				for path, file := range metadata {
					if file.Mode.IsRegular() && file != after[path] {
						t.Fatal("repair rewrote original metadata", path)
					}
				}
				replayed := migrationRecoveryInventory(t, engine.config.SkillStateRoot, fixture.account, fixture.backup)
				request.Payload["lease_attempt"] = 2
				if _, err := NewEngine(engine.config).Execute(ctx, request); err != nil {
					t.Fatal("completed repair could not revalidate", err)
				}
				if _, err := NewEngine(engine.config).Execute(ctx, fixture.request); err == nil {
					t.Fatal("original replay escaped permanent repair fence")
				}
				if !reflect.DeepEqual(replayed, migrationRecoveryInventory(t, engine.config.SkillStateRoot, fixture.account, fixture.backup)) {
					t.Fatal("replay mutated retained state")
				}
				afterLaunches, err := os.ReadFile(fixture.launches)
				if err != nil || string(afterLaunches) != string(launches) {
					t.Fatal("repair launched another writer", err)
				}
			})
		}
	}
}
