package skillmanager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrationRepairFixture(t *testing.T) (*os.Root, string, AccountMigrationRepairIntent, MigrationWriterReceipt) {
	t.Helper()
	root, path := privateStore(t)
	baseline := baselineFixture(t, root)
	if err := BeginAccountMigrationBaseline(root, baseline); err != nil {
		t.Fatal(err)
	}
	original := baseline.Migration
	completeAttestedWriters(t, root, original, "succeeded")
	if err := BeginMigrationOwnership(root, MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: "rollback", Backend: "docker_sandbox"}); err != nil {
		t.Fatal(err)
	}
	pending, err := BeginMigrationWriter(root, MigrationWriterIdentity{Copy: original.Copy, Phase: "rollback-0", CommandDigest: strings.Repeat("f", 64)})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := PrepareAccountMigrationRepair(root, original, "99999999-9999-4999-8999-999999999999", "docker_sandbox", "native")
	if err != nil {
		t.Fatal(err)
	}
	return root, path, intent, pending
}

func repairAttemptFixture(t *testing.T, intent AccountMigrationRepairIntent, lease int64) AccountMigrationRepairAttempt {
	t.Helper()
	task := "recover_tool_account_runtime:" + intent.Migration.Copy.AccountID + ":88888888-8888-4888-8888-888888888888"
	attempt, err := NewAccountMigrationRepairAttempt(intent, task, "77777777-7777-4777-8777-777777777777", lease, "66666666-6666-4666-8666-666666666666")
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestMigrationRepairCompletionSettlesOnlyIndependentHistory(t *testing.T) {
	root, _, intent, pending := migrationRepairFixture(t)
	original := intent.Migration
	checkPending := func() {
		t.Helper()
		err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatal("pending history looked absent or settled", err)
		}
	}
	checkPending()
	for range 2 {
		if err := BeginAccountMigrationRepair(root, intent); err != nil {
			t.Fatal(err)
		}
	}
	first := repairAttemptFixture(t, intent, 1)
	for range 2 {
		if err := BeginAccountMigrationRepairAttempt(root, intent, first); err != nil {
			t.Fatal(err)
		}
	}
	checkPending()
	successor := repairAttemptFixture(t, intent, 2)
	successor.BootID = "55555555-5555-4555-8555-555555555555"
	if err := BeginAccountMigrationRepairAttempt(root, intent, successor); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := FinishAccountMigrationRepair(root, successor); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err != nil {
		t.Fatal(err)
	}
	if saved, err := ReadAccountMigration(root, original); err != nil || saved != original || saved.State != "started" {
		t.Fatal("repair rewrote original whole outcome", err)
	}
	if saved, err := ReadMigrationWriter(root, pending.Identity); err != nil || saved != pending || saved.State != "starting" {
		t.Fatal("repair invented original writer outcome", err)
	}
	if err := FinishAccountMigrationRepair(root, first); err == nil {
		t.Fatal("another attempt replaced completed evidence")
	}
	if err := BeginAccountMigrationRepairAttempt(root, intent, repairAttemptFixture(t, intent, 3)); err == nil {
		t.Fatal("completed repair admitted new permission writes")
	}
	completion, err := ReadAccountMigrationRepairCompletion(root, original.Copy.TaskID)
	if err != nil || completion.Attempt != successor {
		t.Fatal("completion lost its original successful attempt", err)
	}
}

func TestMigrationRepairPermanentlyFencesOriginalReceiptWriters(t *testing.T) {
	root, _, intent, pending := migrationRepairFixture(t)
	if err := BeginAccountMigrationRepair(root, intent); err != nil {
		t.Fatal(err)
	}
	original := intent.Migration
	if _, err := ObserveMigrationWriter(root, pending, strings.Repeat("e", 32)); err == nil {
		t.Fatal("original observation changed after repair began")
	}
	identity := pending.Identity
	identity.Phase = "rollback-1"
	if _, err := BeginMigrationWriter(root, identity); err == nil {
		t.Fatal("original writer restarted")
	}
	copied, err := ReadMigrationPhase(root, original.Copy, "copy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinishMigrationWriter(root, copied, "succeeded"); err == nil {
		t.Fatal("original writer finish escaped repair fence")
	}
	for _, err := range []error{
		BeginAccountCopy(root, original.Copy),
		FinishAccountCopy(root, original.Copy, "copied"),
		BeginMigrationOwnership(root, MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: "target", Backend: "native"}),
		AttestAccountMigration(root, original, "succeeded"),
		FinishAccountMigration(root, original, "succeeded"),
	} {
		if err == nil {
			t.Fatal("original journal mutation escaped repair fence")
		}
	}
	if saved, err := ReadAccountMigrationRepairIntent(root, original.Copy.TaskID); err != nil || saved != intent {
		t.Fatal("rejected writer changed original repair evidence", err)
	}
}

func TestMigrationRepairRejectsChangedOrOrphanedEvidence(t *testing.T) {
	for _, fault := range []string{"baseline", "copy", "phase", "original", "intent", "attempt", "completion", "intent-mode", "attempt-link", "completion-hardlink", "orphan-intent", "orphan-original", "orphan-attempt", "unknown-record"} {
		t.Run(fault, func(t *testing.T) {
			root, path, intent, pending := migrationRepairFixture(t)
			if err := BeginAccountMigrationRepair(root, intent); err != nil {
				t.Fatal(err)
			}
			attempt := repairAttemptFixture(t, intent, 1)
			if err := BeginAccountMigrationRepairAttempt(root, intent, attempt); err != nil {
				t.Fatal(err)
			}
			if err := FinishAccountMigrationRepair(root, attempt); err != nil {
				t.Fatal(err)
			}
			task := intent.Migration.Copy.TaskID
			var err error
			switch fault {
			case "baseline":
				baseline, readErr := ReadAccountMigrationBaseline(root, intent.Migration)
				if readErr != nil {
					t.Fatal(readErr)
				}
				baseline.PermissionsDigest = strings.Repeat("e", 64)
				overwriteRepairFixture(t, path, migrationBaselineName(task), baseline)
			case "copy":
				copy := intent.Migration.Copy
				copy.State = "failed"
				overwriteRepairFixture(t, path, accountCopyName(task), copy)
			case "phase":
				pending.InvocationID, pending.State = strings.Repeat("e", 32), "observed"
				overwriteRepairFixture(t, path, migrationWriterName(task, "rollback-0"), pending)
			case "original":
				original := intent.Migration
				original.State = "failed"
				overwriteRepairFixture(t, path, accountMigrationName(task), original)
			case "intent":
				changed := intent
				changed.EvidenceDigest = strings.Repeat("e", 64)
				overwriteRepairFixture(t, path, migrationRepairIntentName(task), changed)
			case "attempt":
				changed := attempt
				changed.LeaseAttempt++
				overwriteRepairFixture(t, path, migrationRepairAttemptName(attempt), changed)
			case "completion":
				overwriteRepairFixture(t, path, migrationRepairCompletionName(task), AccountMigrationRepairCompletion{Version: 2, Attempt: attempt})
			case "intent-mode":
				err = os.Chmod(filepath.Join(path, migrationRepairIntentName(task)), 0644)
			case "attempt-link":
				name := filepath.Join(path, migrationRepairAttemptName(attempt))
				if err = os.Rename(name, name+".held"); err == nil {
					err = os.Symlink(name+".held", name)
				}
			case "completion-hardlink":
				err = os.Link(filepath.Join(path, migrationRepairCompletionName(task)), filepath.Join(t.TempDir(), "alias"))
			case "orphan-intent":
				err = root.Remove(migrationRepairIntentName(task))
			case "orphan-original":
				err = root.Remove(accountMigrationName(task))
			case "orphan-attempt":
				err = root.Remove(migrationRepairAttemptName(attempt))
			case "unknown-record":
				err = os.WriteFile(filepath.Join(path, "migration-repair-unknown.json"), []byte("{}"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, intent.Migration.Copy.NodeID, intent.Migration.Copy.UserID, intent.Migration.Copy.AccountID); err == nil {
				t.Fatal("unverified repair reopened admission")
			}
			if fault != "unknown-record" {
				if _, err := ReadAccountMigrationRepairCompletion(root, task); err == nil {
					t.Fatal("invalid completion remained readable")
				}
			}
		})
	}
}

func overwriteRepairFixture(t *testing.T, path, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRepairRejectsSubstitutedIntentAndAttempt(t *testing.T) {
	for _, fault := range []string{"original-record", "direction", "baseline", "evidence", "task", "record", "lease", "boot", "digest", "missing-intent", "missing-attempt"} {
		t.Run(fault, func(t *testing.T) {
			root, _, intent, _ := migrationRepairFixture(t)
			if fault != "missing-intent" {
				if err := BeginAccountMigrationRepair(root, intent); err != nil {
					t.Fatal(err)
				}
			}
			attempt := repairAttemptFixture(t, intent, 1)
			switch fault {
			case "original-record":
				intent.OriginalTaskRecordID = attempt.TaskRecordID
			case "direction":
				intent.Source, intent.Target = intent.Target, intent.Source
			case "baseline":
				intent.BaselineDigest = strings.Repeat("e", 64)
			case "evidence":
				intent.EvidenceDigest = strings.Repeat("e", 64)
			case "task":
				attempt.TaskID += "extra"
			case "record":
				attempt.TaskRecordID = intent.OriginalTaskRecordID
			case "lease":
				attempt.LeaseAttempt = 0
			case "boot":
				attempt.BootID = ""
			case "digest":
				attempt.RepairDigest = strings.Repeat("e", 64)
			}
			if fault == "missing-attempt" {
				if err := FinishAccountMigrationRepair(root, attempt); err == nil {
					t.Fatal("completion invented an attempt")
				}
			} else if err := BeginAccountMigrationRepairAttempt(root, intent, attempt); err == nil {
				t.Fatal("substituted or unpersisted authority accepted")
			}
		})
	}
}
