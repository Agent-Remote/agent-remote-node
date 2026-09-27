package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func ownershipFixture(t *testing.T, root *os.Root) AccountMigrationReceipt {
	t.Helper()
	record := migrationFixture()
	record.Copy.WriterVersion = 2
	if err := BeginAccountMigration(root, record); err != nil {
		t.Fatal(err)
	}
	if err := BeginAccountCopy(root, record.Copy); err != nil {
		t.Fatal(err)
	}
	finishWriterFixture(t, root, record.Copy, "copy", "succeeded")
	if err := FinishAccountCopy(root, record.Copy, "copied"); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestMigrationOwnershipIntentProtectsRollbackBeforeFirstACL(t *testing.T) {
	root, _ := privateStore(t)
	original := ownershipFixture(t, root)
	target := MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: "target", Backend: "native"}
	if err := BeginMigrationOwnership(root, target); err != nil {
		t.Fatal(err)
	}
	if err := BeginMigrationOwnership(root, target); !errors.Is(err, os.ErrExist) {
		t.Fatal("ownership intent replaced", err)
	}
	for index := 0; index < 3; index++ {
		finishWriterFixture(t, root, original.Copy, "target-"+strconv.Itoa(index), "succeeded")
	}
	if err := requireMigrationWriterOutcomes(root, original, "succeeded"); err != nil {
		t.Fatal("complete target proofs unavailable", err)
	}
	rollback := MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: "rollback", Backend: "docker_sandbox"}
	if err := BeginMigrationOwnership(root, rollback); err != nil {
		t.Fatal(err)
	}
	if err := FinishAccountMigration(root, original, "succeeded"); err == nil {
		t.Fatal("rollback intent without ACLs was ignored")
	}
	if err := FinishAccountMigration(root, original, "failed"); err == nil {
		t.Fatal("unperformed rollback became terminal")
	}
	if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err == nil {
		t.Fatal("rollback intent opened account writes")
	}
	for index := 0; index < 3; index++ {
		finishWriterFixture(t, root, original.Copy, "rollback-"+strconv.Itoa(index), "succeeded")
	}
	if err := FinishAccountMigration(root, original, "failed"); err != nil {
		t.Fatal("complete original rollback could not finish", err)
	}
	if err := BeginMigrationOwnership(root, rollback); err == nil {
		t.Fatal("terminal migration began more ownership writes")
	}
}

func TestMigrationOwnershipAuthorityRejectsDamageAndForeignCopy(t *testing.T) {
	for _, kind := range []string{"owner", "version", "boot", "phase", "json", "symlink", "hardlink", "mode", "copy-missing", "migration-missing"} {
		t.Run(kind, func(t *testing.T) {
			root, path := privateStore(t)
			original := ownershipFixture(t, root)
			intent := MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: "target", Backend: "native"}
			if err := BeginMigrationOwnership(root, intent); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(path, migrationOwnershipName(original.Copy.TaskID, "target"))
			switch kind {
			case "owner":
				intent.Copy.UserID = intent.Copy.NodeID
			case "version":
				intent.Copy.WriterVersion = 1
			case "boot":
				intent.Copy.BootID = intent.Copy.UserID
			case "phase":
				intent.Phase = "other"
			case "json":
				if err := os.WriteFile(name, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("absent", name); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(name, name+".linked"); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(name, 0644); err != nil {
					t.Fatal(err)
				}
			case "copy-missing":
				if err := os.Remove(filepath.Join(path, accountCopyName(original.Copy.TaskID))); err != nil {
					t.Fatal(err)
				}
			case "migration-missing":
				if err := os.Remove(filepath.Join(path, accountMigrationName(original.Copy.TaskID))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadMigrationOwnership(root, intent.Copy, intent.Phase); err == nil {
				t.Fatal("invalid ownership authority accepted")
			}
			if err := BeginMigrationOwnership(root, intent); err == nil {
				t.Fatal("damaged intent replaced")
			}
			if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err == nil {
				t.Fatal("orphaned or incomplete ownership was ignored")
			}
		})
	}
}
