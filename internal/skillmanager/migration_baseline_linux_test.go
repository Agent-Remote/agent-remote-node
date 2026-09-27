package skillmanager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMigrationAttestationRemainsBoundAfterCompletion(t *testing.T) {
	for _, kind := range []string{"missing-baseline", "baseline-rewritten", "attestation-link", "attestation-mode", "orphaned-original"} {
		t.Run(kind, func(t *testing.T) {
			root, path := privateStore(t)
			baseline := baselineFixture(t, root)
			original := baseline.Migration
			if err := BeginAccountMigrationBaseline(root, baseline); err != nil {
				t.Fatal(err)
			}
			completeAttestedWriters(t, root, original, "succeeded")
			if err := AttestAccountMigration(root, original, "succeeded"); err != nil {
				t.Fatal(err)
			}
			if err := FinishAccountMigration(root, original, "succeeded"); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "missing-baseline":
				err = root.Remove(migrationBaselineName(original.Copy.TaskID))
			case "baseline-rewritten":
				baseline.ContentDigest = strings.Repeat("f", 64)
				data, marshalErr := json.Marshal(baseline)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				err = os.WriteFile(filepath.Join(path, migrationBaselineName(original.Copy.TaskID)), data, 0600)
			case "attestation-link":
				name := filepath.Join(path, migrationAttestationName(original.Copy.TaskID))
				if err = os.Rename(name, name+".original"); err == nil {
					err = os.Symlink(name+".original", name)
				}
			case "attestation-mode":
				err = os.Chmod(filepath.Join(path, migrationAttestationName(original.Copy.TaskID)), 0644)
			case "orphaned-original":
				err = root.Remove(accountMigrationName(original.Copy.TaskID))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationWriter(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID, original.Copy.TaskID); err == nil {
				t.Fatal("terminal outcome bypassed damaged attestation")
			}
			if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err == nil {
				t.Fatal("invalid or orphaned completion evidence reopened admission")
			}
		})
	}
}

func baselineFixture(t *testing.T, root *os.Root) AccountMigrationBaseline {
	t.Helper()
	original := migrationFixture()
	original.Version, original.Copy.WriterVersion = 2, 2
	if err := BeginAccountMigration(root, original); err != nil {
		t.Fatal(err)
	}
	return AccountMigrationBaseline{Version: 1, Migration: original,
		Account: MigrationObjectIdentity{Inode: 1}, ContentDigest: strings.Repeat("a", 64), PermissionsDigest: strings.Repeat("b", 64),
		Parents: []MigrationParentPermissions{{Path: "/accounts", Identity: MigrationObjectIdentity{Inode: 2}, Mode: 040700, AttributeDigest: strings.Repeat("c", 64)}}}
}

func completeAttestedWriters(t *testing.T, root *os.Root, original AccountMigrationReceipt, outcome string) {
	t.Helper()
	if err := BeginAccountCopy(root, original.Copy); err != nil {
		t.Fatal(err)
	}
	finishWriterFixture(t, root, original.Copy, "copy", "succeeded")
	if err := FinishAccountCopy(root, original.Copy, "copied"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"target", "rollback"} {
		if phase == "rollback" && outcome == "succeeded" {
			continue
		}
		backend := "native"
		if phase == "rollback" {
			backend = "docker_sandbox"
		}
		if err := BeginMigrationOwnership(root, MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: phase, Backend: backend}); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < 3; index++ {
			finishWriterFixture(t, root, original.Copy, phase+"-"+strconv.Itoa(index), "succeeded")
		}
	}
}

func TestMigrationBaselineRequiredBeforeOwnershipAndAttestationBeforeCompletion(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			root, _ := privateStore(t)
			baseline := baselineFixture(t, root)
			original := baseline.Migration
			if err := BeginAccountMigrationBaseline(root, baseline); err != nil {
				t.Fatal(err)
			}
			if err := AttestAccountMigration(root, original, outcome); err == nil {
				t.Fatal("baseline alone became completion authority")
			}
			completeAttestedWriters(t, root, original, outcome)
			if err := FinishAccountMigration(root, original, outcome); err == nil {
				t.Fatal("phase completion replaced permission attestation")
			}
			if err := AttestAccountMigration(root, original, outcome); err != nil {
				t.Fatal(err)
			}
			if err := AttestAccountMigration(root, original, outcome); err != nil {
				t.Fatal("lost acknowledgement did not replay", err)
			}
			if err := FinishAccountMigration(root, original, outcome); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err != nil {
				t.Fatal(err)
			}
			if err := root.Remove(migrationAttestationName(original.Copy.TaskID)); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, original.Copy.NodeID, original.Copy.UserID, original.Copy.AccountID); err == nil {
				t.Fatal("missing attestation opened writer admission")
			}
		})
	}
}

func TestMigrationBaselineCannotBeAddedAfterCopyOrUpgradedFromOldMigration(t *testing.T) {
	root, _ := privateStore(t)
	baseline := baselineFixture(t, root)
	if err := BeginAccountCopy(root, baseline.Migration.Copy); err != nil {
		t.Fatal(err)
	}
	if err := BeginAccountMigrationBaseline(root, baseline); err == nil {
		t.Fatal("post-copy observation became original baseline")
	}
	finishWriterFixture(t, root, baseline.Migration.Copy, "copy", "succeeded")
	if err := FinishAccountCopy(root, baseline.Migration.Copy, "copied"); err != nil {
		t.Fatal(err)
	}
	if err := BeginMigrationOwnership(root, MigrationOwnershipIntent{Version: 1, Copy: baseline.Migration.Copy, Phase: "target", Backend: "native"}); err == nil {
		t.Fatal("ownership began without original baseline")
	}
	other, _ := privateStore(t)
	original := migrationFixture()
	original.Copy.WriterVersion = 2
	if err := BeginAccountMigration(other, original); err != nil {
		t.Fatal(err)
	}
	if err := BeginAccountMigrationBaseline(other, baseline); err == nil {
		t.Fatal("historical migration was upgraded to attested authority")
	}
	saved, err := ReadAccountMigration(other, baseline.Migration)
	if err != nil || saved.Version != 1 {
		t.Fatal("legacy original version was not preserved", err)
	}
}

func TestMigrationBaselineDamageAndOrphansBlockAdmission(t *testing.T) {
	for _, kind := range []string{"missing-original", "json", "symlink", "hardlink", "mode", "changed-binding"} {
		t.Run(kind, func(t *testing.T) {
			root, path := privateStore(t)
			baseline := baselineFixture(t, root)
			if err := BeginAccountMigrationBaseline(root, baseline); err != nil {
				t.Fatal(err)
			}
			if err := BeginAccountMigrationBaseline(root, baseline); !errors.Is(err, os.ErrExist) {
				t.Fatal("immutable baseline replaced", err)
			}
			name := filepath.Join(path, migrationBaselineName(baseline.Migration.Copy.TaskID))
			var err error
			switch kind {
			case "missing-original":
				err = root.Remove(accountMigrationName(baseline.Migration.Copy.TaskID))
			case "json":
				err = os.WriteFile(name, []byte("{}"), 0600)
			case "symlink":
				if err = os.Remove(name); err == nil {
					err = os.Symlink("absent", name)
				}
			case "hardlink":
				err = os.Link(name, name+".alias")
			case "mode":
				err = os.Chmod(name, 0644)
			case "changed-binding":
				baseline.Migration.Copy.BootID = baseline.Migration.Copy.UserID
				if _, err := ReadAccountMigrationBaseline(root, baseline.Migration); err == nil {
					t.Fatal("foreign boot accepted")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountMigrationHistory(root, baseline.Migration.Copy.NodeID, baseline.Migration.Copy.UserID, baseline.Migration.Copy.AccountID); err == nil {
				t.Fatal("damaged or orphaned baseline ignored")
			}
		})
	}
}
