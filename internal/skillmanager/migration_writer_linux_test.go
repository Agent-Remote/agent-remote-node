package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writerFixture(t *testing.T, root *os.Root) MigrationWriterIdentity {
	t.Helper()
	copy := accountCopyFixture()
	copy.WriterVersion = 1
	if err := BeginAccountCopy(root, copy); err != nil {
		t.Fatal(err)
	}
	return MigrationWriterIdentity{Copy: copy, Phase: "copy", CommandDigest: strings.Repeat("a", 64)}
}

func TestMigrationWriterIntentObservationAndTerminalAreDurableAndImmutable(t *testing.T) {
	root, path := privateStore(t)
	identity := writerFixture(t, root)
	record, err := BeginMigrationWriter(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != "starting" || record.InvocationID != "" || !validSkillUUID(record.LaunchID) {
		t.Fatal("invalid initial launch authority")
	}
	if _, err := BeginMigrationWriter(root, identity); !errors.Is(err, os.ErrExist) {
		t.Fatal("writer intent replaced", err)
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	invocation := strings.Repeat("b", 32)
	observed, err := ObserveMigrationWriter(reopened, record, invocation)
	if err != nil || observed.State != "observed" {
		t.Fatal("observation not saved", err)
	}
	if _, err := ObserveMigrationWriter(reopened, record, strings.Repeat("c", 32)); err == nil {
		t.Fatal("invocation replaced")
	}
	if _, err := FinishMigrationWriter(reopened, record, "succeeded"); err == nil {
		t.Fatal("stale unobserved record completed writer")
	}
	final, err := FinishMigrationWriter(reopened, observed, "succeeded")
	if err != nil || final.State != "succeeded" {
		t.Fatal("terminal record missing", err)
	}
	if again, err := FinishMigrationWriter(reopened, observed, "succeeded"); err != nil || again != final {
		t.Fatal("terminal replay differs", err)
	}
	if _, err := FinishMigrationWriter(reopened, observed, "failed"); err == nil {
		t.Fatal("terminal outcome changed")
	}
	otherBoot := observed
	otherBoot.Identity.Copy.BootID = identity.Copy.UserID
	if _, err := FinishMigrationWriter(reopened, otherBoot, "succeeded"); err == nil {
		t.Fatal("different boot completed writer")
	}
	saved, err := ReadMigrationWriter(reopened, identity)
	if err != nil || saved != final {
		t.Fatal("saved authority differs", err)
	}
}

func TestMigrationWriterRejectsCorruptionAndForeignBindings(t *testing.T) {
	for _, kind := range []string{"owner", "command", "phase", "json", "symlink", "hardlink", "mode", "copy-missing", "copy-changed"} {
		t.Run(kind, func(t *testing.T) {
			root, path := privateStore(t)
			identity := writerFixture(t, root)
			record, err := BeginMigrationWriter(root, identity)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(path, migrationWriterName(identity.Copy.TaskID, identity.Phase))
			switch kind {
			case "owner":
				identity.Copy.UserID = identity.Copy.NodeID
			case "command":
				identity.CommandDigest = strings.Repeat("c", 64)
			case "phase":
				identity.Phase = "target-0"
			case "json":
				err = os.WriteFile(name, []byte("{}"), 0600)
			case "symlink":
				if err = os.Remove(name); err == nil {
					err = os.Symlink("absent", name)
				}
			case "hardlink":
				err = os.Link(name, name+".linked")
			case "mode":
				err = os.Chmod(name, 0644)
			case "copy-missing":
				err = os.Remove(filepath.Join(path, accountCopyName(identity.Copy.TaskID)))
			case "copy-changed":
				altered := identity.Copy
				altered.InputDigest = strings.Repeat("f", 64)
				err = os.Remove(filepath.Join(path, accountCopyName(identity.Copy.TaskID)))
				if err == nil {
					err = BeginAccountCopy(root, altered)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadMigrationWriter(root, identity); err == nil {
				t.Fatal("invalid writer authority accepted")
			}
			record.Identity = identity
			if _, err := ObserveMigrationWriter(root, record, strings.Repeat("b", 32)); err == nil {
				t.Fatal("invalid authority updated")
			}
		})
	}
}

func finishWriterFixture(t *testing.T, root *os.Root, copy AccountCopyReceipt, phase, outcome string) {
	t.Helper()
	identity := MigrationWriterIdentity{Copy: copy, Phase: phase, CommandDigest: strings.Repeat("a", 64)}
	writer, err := BeginMigrationWriter(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	writer, err = ObserveMigrationWriter(root, writer, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = FinishMigrationWriter(root, writer, outcome); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationWriterVersionRequiresCompletePhaseProofAndProtectsOrphans(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			root, path := privateStore(t)
			migration := migrationFixture()
			migration.Copy.WriterVersion = 1
			if err := BeginAccountMigration(root, migration); err != nil {
				t.Fatal(err)
			}
			if err := BeginAccountCopy(root, migration.Copy); err != nil {
				t.Fatal(err)
			}
			if err := FinishAccountCopy(root, migration.Copy, "copied"); err == nil {
				t.Fatal("copy completion accepted without writer proof")
			}
			finishWriterFixture(t, root, migration.Copy, "copy", "succeeded")
			if err := FinishAccountCopy(root, migration.Copy, "copied"); err != nil {
				t.Fatal(err)
			}
			if err := FinishAccountMigration(root, migration, outcome); err == nil {
				t.Fatal("migration completed without ACL proofs")
			}
			phase := "target"
			if outcome == "failed" {
				finishWriterFixture(t, root, migration.Copy, "target-0", "failed")
				phase = "rollback"
			}
			for index := 0; index < 3; index++ {
				finishWriterFixture(t, root, migration.Copy, phase+"-"+strconv.Itoa(index), "succeeded")
			}
			if err := FinishAccountMigration(root, migration, outcome); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, migration.Copy.NodeID, migration.Copy.UserID, migration.Copy.AccountID); err != nil {
				t.Fatal("complete history blocked", err)
			}
			file := filepath.Join(path, migrationWriterName(migration.Copy.TaskID, phase+"-2"))
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, migration.Copy.NodeID, migration.Copy.UserID, migration.Copy.AccountID); err == nil {
				t.Fatal("missing required phase bypassed gate")
			}
			for _, name := range []string{accountCopyName(migration.Copy.TaskID), accountMigrationName(migration.Copy.TaskID)} {
				if err := os.Remove(filepath.Join(path, name)); err != nil {
					t.Fatal(err)
				}
			}
			if err := CheckAccountMigrationHistory(root, migration.Copy.NodeID, migration.Copy.UserID, migration.Copy.AccountID); err == nil {
				t.Fatal("orphaned writers silently ignored")
			}
		})
	}
}
