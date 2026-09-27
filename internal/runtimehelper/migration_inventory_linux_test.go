package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func migrationInventoryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"empty", ".claude"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "data"), []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, ".claude", "data"), filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/unavailable/external/target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

func inventoryForTest(t *testing.T, root string) migrationInventory {
	t.Helper()
	result, err := scanMigrationInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMigrationInventorySeparatesContentPermissionsAndIdentity(t *testing.T) {
	a, b := migrationInventoryFixture(t), migrationInventoryFixture(t)
	first, second := inventoryForTest(t, a), inventoryForTest(t, b)
	if first.content != second.content || first.permissions != second.permissions || first.observation == second.observation {
		t.Fatal("independent copies lost content/permission identity or reused inode evidence")
	}
	if again := inventoryForTest(t, a); again != first {
		t.Fatal("read changed private inventory")
	}
	if err := os.Chmod(filepath.Join(b, ".claude", "data"), 0640); err != nil {
		t.Fatal(err)
	}
	mode := inventoryForTest(t, b)
	if mode.content != second.content || mode.permissions == second.permissions || mode.observation == second.observation {
		t.Fatal("mode change was not isolated from content")
	}
	if err := unix.Setxattr(filepath.Join(b, ".claude", "data"), "user.inventory", []byte("private attribute"), 0); err != nil {
		t.Fatal(err)
	}
	attrs := inventoryForTest(t, b)
	if attrs.content == mode.content || attrs.permissions == mode.permissions {
		t.Fatal("data xattrs missing from content or permission evidence")
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(filepath.Join(b, ".claude", "data"), 12345, 12345); err != nil {
			t.Fatal(err)
		}
		owner := inventoryForTest(t, b)
		if owner.content != attrs.content || owner.permissions == attrs.permissions {
			t.Fatal("owner missing from permission evidence")
		}
	}
}

func TestMigrationInventoryDetectsCompleteTreeChanges(t *testing.T) {
	for _, kind := range []string{"bytes", "empty-directory", "extra-file", "link-target", "hardlink-topology", "file-type"} {
		t.Run(kind, func(t *testing.T) {
			root := migrationInventoryFixture(t)
			before := inventoryForTest(t, root)
			var err error
			switch kind {
			case "bytes":
				err = os.WriteFile(filepath.Join(root, "hardlink"), []byte("changed fixture"), 0600)
			case "empty-directory":
				err = os.Remove(filepath.Join(root, "empty"))
			case "extra-file":
				err = os.WriteFile(filepath.Join(root, "extra"), nil, 0600)
			case "link-target":
				if err = os.Remove(filepath.Join(root, "link")); err == nil {
					err = os.Symlink("/different/target", filepath.Join(root, "link"))
				}
			case "hardlink-topology":
				if err = os.Remove(filepath.Join(root, "hardlink")); err == nil {
					err = os.WriteFile(filepath.Join(root, "hardlink"), []byte("private fixture"), 0600)
				}
			case "file-type":
				if err = os.Remove(filepath.Join(root, "empty")); err == nil {
					err = os.WriteFile(filepath.Join(root, "empty"), nil, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if after := inventoryForTest(t, root); after.content == before.content {
				t.Fatal("changed tree retained content identity")
			}
		})
	}
}

func TestMigrationInventoryAllowsACLChangesButDetectsDataAttributeLoss(t *testing.T) {
	setfacl, err := exec.LookPath("setfacl")
	if err != nil {
		t.Skip("requires Linux ACL tools")
	}
	root := migrationInventoryFixture(t)
	before := inventoryForTest(t, root)
	path := filepath.Join(root, "hardlink")
	if err := exec.Command(setfacl, "-m", "u:12346:r--", "--", path).Run(); err != nil {
		t.Fatal("fixture ACL failed", err)
	}
	after := inventoryForTest(t, root)
	if after.content != before.content || after.permissions == before.permissions {
		t.Fatal("ACL mutation changed content or lost permission evidence")
	}
	if err := unix.Setxattr(path, "user.private", []byte("retained data"), 0); err != nil {
		t.Fatal(err)
	}
	withData := inventoryForTest(t, root)
	if withData.content == after.content {
		t.Fatal("lost data xattr could match a backup")
	}
}

func TestMigrationInventoryRejectsUnsafeAndUnboundedTrees(t *testing.T) {
	for _, kind := range []string{"external-hardlink", "fifo", "root-link", "ancestor-link", "depth", "path", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := migrationInventoryFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var err error
			switch kind {
			case "external-hardlink":
				err = os.Link(filepath.Join(root, "hardlink"), filepath.Join(t.TempDir(), "outside"))
			case "fifo":
				err = unix.Mkfifo(filepath.Join(root, "fifo"), 0600)
			case "root-link", "ancestor-link":
				link := filepath.Join(t.TempDir(), "linked")
				err = os.Symlink(root, link)
				root = link
				if kind == "ancestor-link" {
					root = filepath.Join(root, ".claude")
				}
			case "depth":
				path := root
				for i := 0; i <= migrationInventoryDepth && err == nil; i++ {
					path = filepath.Join(path, "d")
					err = os.Mkdir(path, 0700)
				}
			case "path":
				// Build through relative descriptors so the kernel PATH_MAX does not
				// hide the scanner's independent relative-path limit.
				var fd int
				fd, err = unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY, 0)
				if err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 18 && err == nil; i++ {
					name := strings.Repeat("a", 240)
					err = unix.Mkdirat(fd, name, 0700)
					if err != nil {
						break
					}
					var next int
					next, err = unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
					unix.Close(fd)
					fd = next
				}
				unix.Close(fd)
			case "cancelled":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := scanMigrationInventory(ctx, root); !errors.Is(err, errMigrationInventory) {
				t.Fatal("unsafe tree accepted", err)
			}
		})
	}
}

func TestMigrationInventoryRejectsNestedMount(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" {
		t.Skip("requires disposable privileged Linux mount namespace")
	}
	root := migrationInventoryFixture(t)
	target := filepath.Join(root, "empty")
	if err := unix.Mount(t.TempDir(), target, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(target, 0)
	if _, err := scanMigrationInventory(context.Background(), root); err == nil {
		t.Fatal("nested bind mount accepted")
	}
}

func TestMigrationInventoryRequiresIndependentBackupRoot(t *testing.T) {
	root := migrationInventoryFixture(t)
	if _, err := compareMigrationInventory(context.Background(), root, root); err == nil {
		t.Fatal("same account root counted as its own backup")
	}
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" {
		t.Skip("bind alias requires disposable privileged Linux mount namespace")
	}
	alias := t.TempDir()
	if err := unix.Mount(root, alias, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(alias, 0)
	if _, err := compareMigrationInventory(context.Background(), root, alias); err == nil {
		t.Fatal("bind alias counted as an independent backup")
	}
}

func TestMigrationRecoveryRejectsIncompleteBackup(t *testing.T) {
	for _, kind := range []string{"missing", "different", "extra", "link", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			engine, original, _, _, backup := completedMigrationFixture(t, "target")
			var err error
			switch kind {
			case "missing":
				err = os.Remove(filepath.Join(backup, "original"))
			case "different":
				err = os.WriteFile(filepath.Join(backup, "original"), []byte("different"), 0600)
			case "extra":
				err = os.Mkdir(filepath.Join(backup, "extra"), 0700)
			case "link":
				err = os.Symlink("/outside", filepath.Join(backup, "extra"))
			case "hardlink":
				err = os.Link(filepath.Join(backup, "original"), filepath.Join(t.TempDir(), "outside"))
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = engine.Execute(context.Background(), explicitRecoveryRequest(original, engine.config.NodeID))
			if !errors.Is(err, errMigrationWritersUnknown) {
				t.Fatal("incomplete backup permitted recovery", err)
			}
		})
	}
}
