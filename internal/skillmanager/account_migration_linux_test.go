package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrationFixture() AccountMigrationReceipt {
	return AccountMigrationReceipt{Version: 1, Copy: accountCopyFixture(), InputDigest: strings.Repeat("c", 64), State: "started"}
}
func TestAccountMigrationRequiresBothTerminalMigrationAndOriginalCopy(t *testing.T) {
	for _, phase := range []string{"intent", "copy", "succeeded", "failed"} {
		t.Run(phase, func(t *testing.T) {
			root, path := privateStore(t)
			r := migrationFixture()
			f := accountFenceFixture()
			if _, err := CloseAccountImports(root, f); err != nil {
				t.Fatal(err)
			}
			if err := BeginAccountMigration(root, r); err != nil {
				t.Fatal(err)
			}
			if phase != "intent" {
				if err := BeginAccountCopy(root, r.Copy); err != nil {
					t.Fatal(err)
				}
				if err := FinishAccountCopy(root, r.Copy, "copied"); err != nil {
					t.Fatal(err)
				}
			}
			terminal := phase == "succeeded" || phase == "failed"
			if terminal {
				if err := FinishAccountMigration(root, r, phase); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			err = CheckAccountMigrationWriter(reopened, f.NodeID, f.UserID, f.AccountID, r.Copy.TaskID)
			if (err == nil) != terminal {
				t.Fatal(phase, err)
			}
			err = CheckAccountCopyHistory(reopened, f.NodeID, f.UserID, f.AccountID)
			if (err == nil) != terminal {
				t.Fatal("local inventory", phase, err)
			}
			changed := r
			changed.InputDigest = strings.Repeat("d", 64)
			if _, err := ReadAccountMigration(reopened, changed); err == nil {
				t.Fatal("changed input accepted")
			}
			if err := BeginAccountMigration(reopened, r); !errors.Is(err, os.ErrExist) {
				t.Fatal("intent replaced", err)
			}
			if err := CheckAccountMigrationWriter(reopened, f.NodeID, f.NodeID, f.AccountID, r.Copy.TaskID); err == nil {
				t.Fatal("wrong owner accepted")
			}
			if terminal {
				if err := os.Remove(filepath.Join(path, accountCopyName(r.Copy.TaskID))); err != nil {
					t.Fatal(err)
				}
				if err := CheckAccountCopyHistory(reopened, f.NodeID, f.UserID, f.AccountID); err == nil {
					t.Fatal("missing copy promoted to proof")
				}
			}
		})
	}
}
func TestAccountMigrationCorruptionAndTerminalReclassificationFailClosed(t *testing.T) {
	for _, kind := range []string{"json", "link", "mode", "identity", "outcome", "boot"} {
		t.Run(kind, func(t *testing.T) {
			root, path := privateStore(t)
			r := migrationFixture()
			if err := BeginAccountMigration(root, r); err != nil {
				t.Fatal(err)
			}
			if err := BeginAccountCopy(root, r.Copy); err != nil {
				t.Fatal(err)
			}
			if err := FinishAccountCopy(root, r.Copy, "copied"); err != nil {
				t.Fatal(err)
			}
			if err := FinishAccountMigration(root, r, "succeeded"); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(path, accountMigrationName(r.Copy.TaskID))
			switch kind {
			case "json":
				if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "link":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("absent", file); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "identity":
				r.Copy.UserID = r.Copy.NodeID
			case "outcome":
				if err := FinishAccountMigration(root, r, "failed"); err == nil {
					t.Fatal("terminal outcome replaced")
				}
				return
			case "boot":
				r.Copy.BootID = r.Copy.UserID
				if err := FinishAccountMigration(root, r, "succeeded"); err == nil {
					t.Fatal("new boot completed old writer")
				}
				return
			}
			if _, err := ReadAccountMigration(root, r); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("corrupt proof accepted", err)
			}
		})
	}
}
