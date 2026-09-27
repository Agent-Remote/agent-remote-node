package runtimehelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func TestMigrationBaselineRejectsChangedHistoricalContentAndPermissions(t *testing.T) {
	for _, kind := range []string{"unchanged", "target-mode", "backup-mode", "both-bytes", "parent-acl", "parent-replaced", "account-replaced", "missing-baseline", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			engine, binding, _ := accountTakeoverFixture(t)
			account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
			backup := filepath.Join(engine.config.StateRoot, "backup")
			for _, directory := range []string{account, backup} {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(account, "data"), []byte("original private fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			task := "migrate_tool_account_runtime:" + binding.AccountID + ":77777777-7777-4777-8777-777777777777"
			original := engine.accountMigrationReceipt(task, binding.UserID, binding.AccountID, "docker_sandbox", "native", account, backup)
			store, err := engine.openSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := skillmanager.BeginAccountMigration(store, original); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if kind != "missing-baseline" {
				if err := engine.captureMigrationBaseline(ctx, store, original, account); err != nil {
					t.Fatal(err)
				}
			}
			if err := exec.Command("/bin/cp", "--archive", "--", account+"/.", backup+"/").Run(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "target-mode":
				err = os.Chmod(filepath.Join(account, "data"), 0640)
			case "backup-mode":
				err = os.Chmod(filepath.Join(backup, "data"), 0640)
			case "both-bytes":
				for _, root := range []string{account, backup} {
					if err := os.WriteFile(filepath.Join(root, "data"), []byte("mutually matching replacement"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "parent-acl":
				err = exec.Command("/usr/bin/setfacl", "-m", "u:12345:--x", "--", filepath.Dir(account)).Run()
			case "parent-replaced", "account-replaced":
				path := account
				if kind == "parent-replaced" {
					path = filepath.Dir(account)
				}
				if err = os.Rename(path, path+"-original"); err == nil {
					err = exec.Command("/bin/cp", "--archive", "--", path+"-original", path).Run()
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = engine.inspectMigrationCompletion(ctx, store, original, account, backup, "succeeded")
			wantTarget := kind == "unchanged" || kind == "target-mode" || kind == "parent-acl"
			if (err == nil) != wantTarget {
				t.Fatal("wrong target attestation eligibility", err)
			}
			_, err = engine.inspectMigrationCompletion(ctx, store, original, account, backup, "failed")
			if (err == nil) != (kind == "unchanged") {
				t.Fatal("source rollback accepted changed original permissions or content", err)
			}
		})
	}
}

func TestMigrationBaselineCapturesOriginalParentACLAndRejectsLinkedAncestor(t *testing.T) {
	engine, binding, _ := accountTakeoverFixture(t)
	account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
	if err := os.MkdirAll(account, 0700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(account)
	if err := exec.Command("/usr/bin/setfacl", "-m", "u:12345:r-x,d:u:12345:r-x", "--", parent).Run(); err != nil {
		t.Fatal(err)
	}
	permissions, err := engine.migrationParentPermissions(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	last := permissions[len(permissions)-1]
	if len(last.AccessACL) == 0 || len(last.DefaultACL) == 0 || last.Mode&unix.S_IFMT != unix.S_IFDIR {
		t.Fatal("original ACL values missing")
	}
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent+"-original", parent); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.migrationParentPermissions(context.Background(), account); err == nil {
		t.Fatal("linked ancestor became original permission authority")
	}
}
