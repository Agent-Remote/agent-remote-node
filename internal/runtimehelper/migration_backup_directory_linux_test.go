package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationBackupCreationNeverReusesOrRepairsUntrustedDestination(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires private root-owned state directories")
	}
	for _, kind := range []string{"fresh", "traversable-state", "worker-group-parent", "existing", "leaf-link", "parent-link", "ancestor-link", "public-parent", "writable-state", "foreign-parent", "foreign-state", "different-path", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			outside := filepath.Join(root, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			engine := NewEngine(EngineConfig{StateRoot: state})
			task := "fixture"
			parent := filepath.Join(state, "migrations")
			backup := filepath.Join(parent, shortDigest(task, 32))
			if kind != "fresh" && kind != "ancestor-link" && kind != "cancelled" {
				if err := os.MkdirAll(parent, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch kind {
			case "traversable-state":
				err = os.Chmod(state, 0711)
			case "worker-group-parent":
				err = os.Chown(parent, 0, 22000)
			case "existing":
				if err = os.Mkdir(backup, 0700); err == nil {
					err = os.WriteFile(filepath.Join(backup, "retained"), []byte("unique backup"), 0600)
				}
			case "leaf-link":
				err = os.Symlink(outside, backup)
			case "parent-link":
				if err = os.Remove(parent); err == nil {
					err = os.Symlink(outside, parent)
				}
			case "ancestor-link":
				err = os.Symlink(outside, state)
			case "public-parent":
				err = os.Chmod(parent, 0755)
			case "writable-state":
				err = os.Chmod(state, 0770)
			case "foreign-parent":
				err = os.Chown(parent, 12345, 12345)
			case "foreign-state":
				err = os.Chown(state, 12345, 12345)
			case "different-path":
				backup = filepath.Join(outside, "selected")
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			err = engine.createMigrationBackupDirectory(ctx, task, backup)
			if (err == nil) != (kind == "fresh" || kind == "traversable-state" || kind == "worker-group-parent") {
				t.Fatal("wrong backup creation eligibility", err)
			}
			if kind == "fresh" {
				if err := engine.createMigrationBackupDirectory(ctx, task, backup); err == nil {
					t.Fatal("empty existing backup was reused")
				}
			}
			if kind == "cancelled" {
				if _, err := os.Lstat(state); !os.IsNotExist(err) {
					t.Fatal("cancelled request created migration state", err)
				}
			}
			if kind == "existing" {
				data, err := os.ReadFile(filepath.Join(backup, "retained"))
				if err != nil || string(data) != "unique backup" {
					t.Fatal("retained backup changed", err)
				}
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("untrusted destination was touched", err)
			}
			if kind == "public-parent" {
				info, err := os.Stat(parent)
				if err != nil || info.Mode().Perm() != 0755 {
					t.Fatal("existing parent permissions were repaired", err)
				}
			}
		})
	}
}
