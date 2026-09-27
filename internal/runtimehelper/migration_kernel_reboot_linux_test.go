package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

type kernelMigrationInput struct {
	VerifySource bool                             `json:"verify_source"`
	Config       EngineConfig                     `json:"config"`
	Request      Request                          `json:"request"`
	Account      string                           `json:"account"`
	Backup       string                           `json:"backup"`
	Launches     string                           `json:"launches"`
	BootID       string                           `json:"boot_id"`
	Completed    bool                             `json:"completed"`
	Inventory    map[string]migrationRecoveryFile `json:"inventory"`
}

// This proof uses actual copy/ownership services, then a host-driven power cut and a new kernel.
func TestMigrationActualKernelReboot(t *testing.T) {
	phase := os.Getenv("AGENT_REMOTE_SKILL_KERNEL_REBOOT_PHASE")
	if phase == "" {
		t.Skip("requires disposable two-boot VM")
	}
	if phase == "seed" {
		seedMigrationKernelReboot(t)
		return
	}
	if phase != "recover" {
		t.Fatal("unknown migration kernel phase")
	}
	data, err := os.ReadFile(kernelRebootReceipt)
	if err != nil {
		t.Fatal(err)
	}
	var saved []kernelMigrationInput
	if err := json.Unmarshal(data, &saved); err != nil || len(saved) != 5 {
		t.Fatal("migration kernel inputs missing", err)
	}
	boot := currentBootID()
	for _, input := range saved {
		if !validSkillUUID(boot) || boot == input.BootID {
			t.Fatal("migration kernel did not actually change")
		}
		engine := NewEngine(input.Config)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		request := explicitRecoveryRequest(input.Request, input.Config.NodeID)
		if input.VerifySource {
			request.Payload["binding"].(map[string]any)["version"] = 2
			request.Payload["binding"].(map[string]any)["action"] = "verify_source"
		}
		result, err := engine.Execute(ctx, request)
		cancel()
		if input.Completed {
			if err != nil || result["recovered"] != true {
				t.Fatal("durable original migration was not recovered after kernel change", err)
			}
		} else if err == nil {
			t.Fatal("kernel change promoted phase success without whole completion")
		}
		if !reflect.DeepEqual(input.Inventory, migrationRecoveryInventory(t, input.Config.SkillStateRoot, input.Account, input.Backup)) {
			t.Fatal("kernel recovery changed original account, backup or receipts")
		}
		if !input.Completed {
			repairMigrationAfterKernelChange(t, engine, input)
		}
		launches, err := os.ReadFile(input.Launches)
		expectedLaunches := "xxxx"
		if input.VerifySource {
			expectedLaunches = "xxxxx"
		}
		if err != nil || string(launches) != expectedLaunches {
			t.Fatal("kernel recovery launched another migration writer", err)
		}
	}
	fmt.Printf("AR_KERNEL_REBOOT_RECOVERED migration_completed=1 migration_pending=1 source_completed=1 source_pending=1 source_repaired=3 interrupted_writer=1 new_boot=%s\n", boot)
}

func seedMigrationKernelReboot(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", "/var/tmp")
	var saved []kernelMigrationInput
	for index := range 5 {
		fixture := newMigrationCompletionFixture(t, index)
		source := index == 2 || index == 3
		completed := index == 0 || index == 2
		interrupted := index == 4
		if source {
			fixture.engine.config.SetfaclPath = migrationACLFailureCommand(t, "failed-target-acl-residual")
		}
		if interrupted {
			fixture.engine.config.SetfaclPath = delayedFinalMigrationACL(t, fixture.root, filepath.Join(fixture.root, "ready"), filepath.Join(fixture.root, "release"))
		}
		engine := fixture.engine
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if completed {
			_, err := engine.Execute(ctx, fixture.request)
			var expected error
			if source {
				expected = errMigrationFailed
			}
			if !errors.Is(err, expected) {
				cancel()
				t.Fatal("original migration did not complete", err)
			}
		} else {
			seedMigrationWithoutWholeCompletion(t, ctx, fixture, source, interrupted)
		}
		cancel()
		launches, err := os.ReadFile(fixture.launches)
		expectedLaunches := "xxxx"
		if source {
			expectedLaunches = "xxxxx"
		}
		if err != nil || string(launches) != expectedLaunches {
			t.Fatal("original copy and three ACL writers were not executed exactly once", err)
		}
		saved = append(saved, kernelMigrationInput{Config: engine.config, Request: fixture.request, Account: fixture.account, Backup: fixture.backup, Launches: fixture.launches, BootID: currentBootID(), Completed: completed, VerifySource: source,
			Inventory: migrationRecoveryInventory(t, engine.config.SkillStateRoot, fixture.account, fixture.backup)})
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kernelRebootReceipt, data, 0600); err != nil {
		t.Fatal(err)
	}
	unix.Sync()
	fmt.Printf("AR_KERNEL_REBOOT_SEEDED migration_completed=1 migration_pending=1 source_completed=1 source_pending=1 interrupted_writer=1 boot=%s\n", currentBootID())
	<-time.After(4 * time.Minute)
	t.Fatal("migration VM power cut was not performed")
}

func seedMigrationWithoutWholeCompletion(t *testing.T, ctx context.Context, fixture migrationCompletionFixture, source, interrupted bool) {
	t.Helper()
	engine, copy := fixture.engine, fixture.original.Copy
	original := engine.accountMigrationReceipt(copy.TaskID, copy.UserID, copy.AccountID, "docker_sandbox", "native", fixture.account, fixture.backup)
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := skillmanager.BeginAccountMigration(store, original); err != nil {
		t.Fatal(err)
	}
	if err := engine.captureMigrationBaseline(ctx, store, original, fixture.account); err != nil {
		t.Fatal(err)
	}
	if err := engine.createMigrationBackupDirectory(ctx, copy.TaskID, fixture.backup); err != nil {
		t.Fatal(err)
	}
	if err := engine.backupMigratingAccount(ctx, copy.TaskID, copy.UserID, copy.AccountID, "docker_sandbox", "native", fixture.account, fixture.backup); err != nil {
		t.Fatal(err)
	}
	if interrupted {
		work, stop := context.WithCancel(ctx)
		defer stop()
		done := make(chan error, 1)
		go func() {
			done <- engine.applyMigrationOwnership(work, original.Copy, "target", copy.UserID, fixture.account, "native")
		}()
		ready := filepath.Join(fixture.root, "ready")
		for !pathExists(ready) && ctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
		}
		stop()
		if err := <-done; !errors.Is(err, errMigrationWritersUnknown) || !pathExists(ready) {
			t.Fatal("original traversal was not interrupted", err)
		}
		// This actual transient writer remains alive until the external kernel power cut.
		return
	}
	targetErr := engine.applyMigrationOwnership(ctx, original.Copy, "target", copy.UserID, fixture.account, "native")
	if source {
		if targetErr == nil || errors.Is(targetErr, errMigrationWritersUnknown) {
			t.Fatal("expected drained target failure", targetErr)
		}
		if err := engine.applyMigrationOwnership(ctx, original.Copy, "rollback", copy.UserID, fixture.account, "docker_sandbox"); err != nil {
			t.Fatal(err)
		}
		if err := engine.restoreOriginalMigrationPermissions(ctx, store, original, "docker_sandbox", fixture.account, fixture.backup); err != nil {
			t.Fatal(err)
		}
	} else if targetErr != nil {
		t.Fatal(targetErr)
	}
	// Deliberately omit whole completion; no phase or terminal receipt is fabricated.
}

func repairMigrationAfterKernelChange(t *testing.T, engine Engine, input kernelMigrationInput) {
	t.Helper()
	request := explicitSourceRepairRequest(input.Request, input.Config.NodeID)
	if _, err := engine.Execute(context.Background(), request); err != nil {
		t.Fatal("previous-boot source repair rejected", err)
	}
	after := migrationRecoveryInventory(t, input.Config.SkillStateRoot, input.Backup)
	before := input.Inventory
	for path, file := range after {
		original, existed := before[path]
		if existed && file.Mode.IsRegular() && file != original {
			t.Fatal("previous-boot repair changed original receipt or backup", path)
		}
	}
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	completion, err := skillmanager.ReadAccountMigrationRepairCompletion(store, input.Request.RequestID)
	if err != nil || completion.Attempt.BootID != currentBootID() {
		t.Fatal("repair did not bind the new kernel", err)
	}
	intent, err := skillmanager.ReadAccountMigrationRepairIntent(store, input.Request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.inspectMigrationCompletion(context.Background(), store, intent.Migration, input.Account, input.Backup, "failed"); err != nil {
		t.Fatal("new kernel did not restore exact original source", err)
	}
	snapshot := migrationRecoveryInventory(t, input.Config.SkillStateRoot, input.Account, input.Backup)
	request.Payload["lease_attempt"] = 2
	if _, err := NewEngine(input.Config).Execute(context.Background(), request); err != nil {
		t.Fatal("completed repair could not revalidate", err)
	}
	if !reflect.DeepEqual(snapshot, migrationRecoveryInventory(t, input.Config.SkillStateRoot, input.Account, input.Backup)) {
		t.Fatal("completed repair replay mutated files")
	}
}
