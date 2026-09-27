package runtimehelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func migrationParentFixture(t *testing.T, extended bool) skillmanager.MigrationParentPermissions {
	t.Helper()
	path := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(path, 0750); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chmod(path, 02750); err != nil {
		t.Fatal(err)
	}
	if extended {
		migrationParentSetACL(t, path, "u:12343:rwx,g:12344:r-x,m::r--,d:u:12343:rwx,d:m::r-x")
	}
	if err := unix.Setxattr(path, "user.retained", []byte("original attribute"), 0); err != nil {
		t.Fatal(err)
	}
	return observeMigrationParent(t, path)
}

func observeMigrationParent(t *testing.T, path string) skillmanager.MigrationParentPermissions {
	t.Helper()
	scanner := migrationInventoryScanner{ctx: context.Background(), xattrBuffer: make([]byte, 64<<10)}
	parent, err := scanner.parentPermissions(path)
	if err != nil {
		t.Fatal(err)
	}
	return parent
}

func migrationParentSetACL(t *testing.T, path, value string) {
	t.Helper()
	if err := exec.Command("/usr/bin/setfacl", "-m", value, "--", path).Run(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationParentGuardMatchesActualTraversalAndInterruptedRestore(t *testing.T) {
	for _, extended := range []bool{false, true} {
		for _, phase := range []string{"unchanged", "target", "rollback", "target-rollback", "chmod-interrupted"} {
			t.Run(phase+map[bool]string{false: "-simple", true: "-extended"}[extended], func(t *testing.T) {
				original := migrationParentFixture(t, extended)
				target := phase == "target" || phase == "target-rollback" || phase == "chmod-interrupted"
				rollback := phase == "rollback" || phase == "target-rollback"
				if target {
					migrationParentSetACL(t, original.Path, "u:12341:--x,u:12342:--x")
				}
				if rollback {
					migrationParentSetACL(t, original.Path, "u:12345:--x,u:12342:--x")
				}
				if phase == "chmod-interrupted" {
					if err := unix.Chmod(original.Path, original.Mode&07777); err != nil {
						t.Fatal(err)
					}
				}
				states, err := migrationParentAccessStates(original, 12341, 12345, 12342, target, rollback, phase == "chmod-interrupted")
				if err != nil {
					t.Fatal(err)
				}
				before := observeMigrationParent(t, original.Path)
				observed, err := inspectMigrationParentRestoration(context.Background(), original, states)
				if err != nil || !reflect.DeepEqual(before, observed) {
					t.Fatal("original command effect rejected", err)
				}
				if !reflect.DeepEqual(before, observeMigrationParent(t, original.Path)) {
					t.Fatal("preflight changed parent")
				}
				if err := restoreMigrationParent(context.Background(), original, observed); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(original, observeMigrationParent(t, original.Path)) {
					t.Fatal("parent did not return exactly to baseline")
				}
			})
		}
	}
}

func TestMigrationParentGuardPreservesUnrelatedChanges(t *testing.T) {
	for _, fault := range []string{"user", "group", "existing-user", "mask", "default", "mode", "owner", "xattr", "identity", "link", "cancelled", "unrecorded-target", "unrecorded-restore", "late-change"} {
		t.Run(fault, func(t *testing.T) {
			original := migrationParentFixture(t, true)
			migrationParentSetACL(t, original.Path, "u:12341:--x,u:12342:--x")
			states, err := migrationParentAccessStates(original, 12341, 12345, 12342, fault != "unrecorded-target", false, false)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := inspectMigrationParentRestoration(context.Background(), original, states)
			if fault != "unrecorded-target" && err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "user", "late-change":
				migrationParentSetACL(t, original.Path, "u:12346:--x")
			case "group":
				migrationParentSetACL(t, original.Path, "g:12346:--x")
			case "existing-user":
				migrationParentSetACL(t, original.Path, "u:12343:r--")
			case "mask":
				migrationParentSetACL(t, original.Path, "m::---")
			case "default":
				migrationParentSetACL(t, original.Path, "d:u:12346:rwx")
			case "mode":
				err = unix.Chmod(original.Path, 0777)
			case "owner":
				if os.Geteuid() != 0 {
					t.Skip("requires isolated root ownership fixture")
				}
				err = os.Chown(original.Path, 12346, 12346)
			case "xattr":
				err = unix.Setxattr(original.Path, "user.retained", []byte("unrelated change"), 0)
			case "identity", "link":
				if err := os.Rename(original.Path, original.Path+"-held"); err != nil {
					t.Fatal(err)
				}
				if fault == "link" {
					err = os.Symlink(original.Path+"-held", original.Path)
				} else {
					err = os.Mkdir(original.Path, 0750)
				}
			case "unrecorded-restore":
				err = unix.Chmod(original.Path, original.Mode&07777)
			}
			if err != nil && fault != "unrecorded-target" {
				t.Fatal(err)
			}
			before := migrationRecoveryInventory(t, filepath.Dir(original.Path))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancelled" {
				cancel()
			}
			if fault == "late-change" {
				err = restoreMigrationParent(ctx, original, observed)
			} else {
				_, err = inspectMigrationParentRestoration(ctx, original, states)
			}
			if err == nil {
				t.Fatal("unrelated or unproven change accepted")
			}
			if !reflect.DeepEqual(before, migrationRecoveryInventory(t, filepath.Dir(original.Path))) {
				t.Fatal("rejection modified parent state")
			}
			if fault != "link" {
				current := observeMigrationParent(t, original.Path)
				if fault == "late-change" && current.AttributeDigest == original.AttributeDigest && current.Mode == original.Mode {
					t.Fatal("late change was erased")
				}
			}
		})
	}
}

func TestMigrationAccessACLRejectsMalformedAndModeInconsistentEvidence(t *testing.T) {
	original := migrationParentFixture(t, true)
	for _, fault := range []string{"empty", "version", "truncated", "permission", "id", "order", "mode"} {
		t.Run(fault, func(t *testing.T) {
			data := append([]byte{}, original.AccessACL...)
			mode := original.Mode
			switch fault {
			case "empty":
				data = []byte{}
			case "version":
				data[0] = 1
			case "truncated":
				data = data[:len(data)-1]
			case "permission":
				data[6] = 8
			case "id":
				data[8] = 0
			case "order":
				data[12] = 1
			case "mode":
				mode ^= 0400
			}
			if _, err := decodeMigrationAccessACL(data, mode); err == nil {
				t.Fatal("malformed ACL accepted")
			}
		})
	}
}
