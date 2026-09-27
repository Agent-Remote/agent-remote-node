package runtimehelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMigrationRestoresOriginalModesACLsCapabilitiesAndLinkOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root permission restoration")
	}
	account := migrationInventoryFixture(t)
	data := filepath.Join(account, ".claude", "data")
	for _, args := range [][]string{{"-m", "u:12341:r--", "--", data}, {"-m", "u:12341:r-x,d:u:12341:r-x", "--", account}} {
		if err := exec.Command("/usr/bin/setfacl", args...).Run(); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Chmod(data, 06640); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/usr/sbin/setcap", "cap_net_bind_service=ep", data).Run(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(data, "user.fixture", []byte("retained data attribute"), 0); err != nil {
		t.Fatal(err)
	}
	original := inventoryForTest(t, account)
	backup := t.TempDir()
	if err := exec.Command("/bin/cp", "--archive", "--", account+"/.", backup+"/").Run(); err != nil {
		t.Fatal(err)
	}
	backupBefore := inventoryForTest(t, backup)
	if original.content != backupBefore.content || original.permissions != backupBefore.permissions {
		t.Fatal("fixture copy did not preserve complete original permissions")
	}
	if err := filepath.WalkDir(account, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, 12342, 12342)
	}); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/usr/bin/setfacl", "-R", "-P", "-m", "u:12342:rwX", "--", account).Run(); err != nil {
		t.Fatal(err)
	}
	changed := inventoryForTest(t, account)
	if changed.permissions == original.permissions {
		t.Fatal("fixture did not change original permissions")
	}
	if err := restoreMigrationTreePermissions(context.Background(), account, backup, [2]migrationInventory{changed, backupBefore}); err != nil {
		t.Fatal(err)
	}
	restored := inventoryForTest(t, account)
	if restored.content != original.content || restored.permissions != original.permissions || restored.root != original.root {
		t.Fatal("restoration lost original bytes, topology, permission xattrs or root identity")
	}
	if after := inventoryForTest(t, backup); after != backupBefore {
		t.Fatal("restoration changed retained backup")
	}
}

func TestMigrationPermissionRestorationRejectsChangedOrAliasedInputsBeforeWriting(t *testing.T) {
	for _, kind := range []string{"cancelled", "changed-source", "changed-backup", "external-hardlink", "root-alias", "root-link"} {
		t.Run(kind, func(t *testing.T) {
			account := migrationInventoryFixture(t)
			backup := t.TempDir()
			if err := exec.Command("/bin/cp", "--archive", "--", account+"/.", backup+"/").Run(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(account, ".claude", "data"), 0644); err != nil {
				t.Fatal(err)
			}
			expected := [2]migrationInventory{inventoryForTest(t, account), inventoryForTest(t, backup)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var err error
			switch kind {
			case "cancelled":
				cancel()
			case "changed-source":
				err = os.WriteFile(filepath.Join(account, ".claude", "data"), []byte("changed"), 0644)
			case "changed-backup":
				err = os.Chmod(filepath.Join(backup, ".claude", "data"), 0644)
			case "external-hardlink":
				err = os.Link(filepath.Join(account, ".claude", "data"), filepath.Join(t.TempDir(), "outside"))
			case "root-alias":
				backup, expected[1] = account, expected[0]
			case "root-link":
				alias := filepath.Join(t.TempDir(), "alias")
				err = os.Symlink(account, alias)
				account = alias
			}
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(account, ".claude", "data")
			before, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := restoreMigrationTreePermissions(ctx, account, backup, expected); err == nil {
				t.Fatal("unverified permission input accepted")
			}
			after, err := os.Stat(file)
			if err != nil || after.Mode() != before.Mode() {
				t.Fatal("rejected restoration changed source permissions", err)
			}
		})
	}
}

func TestMigrationRestoresOriginalTraversalACLsWithoutReowningParents(t *testing.T) {
	engine, binding, _ := accountTakeoverFixture(t)
	account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
	if err := os.MkdirAll(account, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	parents, err := engine.migrationParentPermissions(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range parents {
		if err := exec.Command("/usr/bin/setfacl", "-m", "u:12342:--x", "--", parent.Path).Run(); err != nil {
			t.Fatal(err)
		}
		states, err := migrationParentAccessStates(parent, 12342, 12342, 12342, true, false, false)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := inspectMigrationParentRestoration(ctx, parent, states)
		if err != nil {
			t.Fatal(err)
		}
		if err := restoreMigrationParent(ctx, parent, observed); err != nil {
			t.Fatal(err)
		}
	}
	after, err := engine.migrationParentPermissions(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	for index, parent := range parents {
		if after[index].Identity != parent.Identity || after[index].Mode != parent.Mode || after[index].UID != parent.UID || after[index].GID != parent.GID || after[index].AttributeDigest != parent.AttributeDigest {
			t.Fatal("parent permissions did not return to their original identity")
		}
	}
}
