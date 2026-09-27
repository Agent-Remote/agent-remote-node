package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type migrationRestoreCancellation struct {
	context.Context
	cancel context.CancelFunc
	path   string
	mode   os.FileMode
}

func (c migrationRestoreCancellation) Err() error {
	// Cancel when the first real permission write is visible, without adding a
	// test callback or a synthetic writer to the production restoration loop.
	info, err := os.Stat(c.path)
	mode := c.mode
	if mode == 0 {
		mode = 0600
	}
	if err == nil && info.Mode().Perm() == mode {
		c.cancel()
	}
	return c.Context.Err()
}

func TestMigrationPermissionRestorationCancellationPreservesBytesAndOriginalBackup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated Linux root ownership writes")
	}
	account, backup := migrationInventoryFixture(t), t.TempDir()
	data := filepath.Join(account, ".claude", "data")
	if err := exec.Command("/bin/cp", "--archive", "--", account+"/.", backup+"/").Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(data, 0644); err != nil {
		t.Fatal(err)
	}
	before := [2]migrationInventory{inventoryForTest(t, account), inventoryForTest(t, backup)}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := migrationRestoreCancellation{Context: base, cancel: cancel, path: data}
	if err := restoreMigrationTreePermissions(ctx, account, backup, before); err == nil {
		t.Fatal("mid-write cancellation became completed restoration")
	}
	if !errors.Is(base.Err(), context.Canceled) {
		t.Fatal("fixture did not interrupt an actual permission write")
	}
	if after := inventoryForTest(t, account); after.content != before[0].content {
		t.Fatal("interrupted restoration changed account bytes or topology")
	}
	if after := inventoryForTest(t, backup); after != before[1] {
		t.Fatal("interrupted restoration changed retained backup evidence")
	}
}

func TestMigrationParentRestorationCancellationResumesOnlyAttributablePartialACL(t *testing.T) {
	parent := migrationParentFixture(t, false)
	if err := os.Chmod(parent.Path, 0700); err != nil {
		t.Fatal(err)
	}
	original := observeMigrationParent(t, parent.Path)
	migrationParentSetACL(t, original.Path, "u:12341:--x,u:12342:--x")
	states, err := migrationParentAccessStates(original, 12341, 12341, 12342, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := inspectMigrationParentRestoration(context.Background(), original, states)
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := migrationRestoreCancellation{Context: base, cancel: cancel, path: original.Path, mode: 0700}
	if err := restoreMigrationParent(ctx, original, observed); err == nil || !errors.Is(base.Err(), context.Canceled) {
		t.Fatal("did not interrupt between actual chmod and ACL restore", err)
	}
	if _, err := inspectMigrationParentRestoration(context.Background(), original, states); err == nil {
		t.Fatal("unattributed partial restoration admitted")
	}
	resumed, err := migrationParentAccessStates(original, 12341, 12341, 12342, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	observed, err = inspectMigrationParentRestoration(context.Background(), original, resumed)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreMigrationParent(context.Background(), original, observed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, observeMigrationParent(t, original.Path)) {
		t.Fatal("resumed parent lost original attributes")
	}
}
