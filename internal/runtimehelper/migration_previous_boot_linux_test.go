package runtimehelper

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestMigrationPreviousBootRecoveryRequiresDurableWholeSuccess(t *testing.T) {
	for _, kind := range []string{"completed", "started", "missing-backup", "incomplete-backup", "invalid-account", "foreign-unit", "late-unit", "empty-cgroup", "populated-cgroup", "linked-cgroup", "rollback-intent", "missing-phase", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			engine, request, original, root, backup := completedMigrationFixture(t, "previous-boot")
			store, err := engine.openSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if kind != "started" && kind != "rollback-intent" {
				if err := skillmanager.FinishAccountMigration(store, original, "succeeded"); err != nil {
					t.Fatal(err)
				}
			}
			account := filepath.Join(engine.config.AccountRoot, original.Copy.UserID, "tool-accounts", "claude", original.Copy.AccountID)
			preparePreviousBootFailure(t, engine, store, original, kind, root, account, backup)
			before := migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			result, err := NewEngine(engine.config).Execute(ctx, explicitRecoveryRequest(request, engine.config.NodeID))
			if kind == "completed" {
				if err != nil || result["recovered"] != true {
					t.Fatal("durable previous-boot success was not revalidated", err)
				}
			} else if err == nil {
				t.Fatal("incomplete previous-boot authority reopened admission")
			}
			after := migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("previous-boot inspection changed original files or receipts")
			}
			calls, _ := os.ReadFile(filepath.Join(root, "calls"))
			for _, call := range strings.Fields(string(calls)) {
				if call != "show" {
					t.Fatal("previous-boot recovery invoked a mutating command")
				}
			}
		})
	}
}

func preparePreviousBootFailure(t *testing.T, engine Engine, store *os.Root, original skillmanager.AccountMigrationReceipt, kind, root, account, backup string) {
	t.Helper()
	var err error
	switch kind {
	case "missing-backup":
		err = os.RemoveAll(backup)
	case "incomplete-backup":
		err = os.Remove(filepath.Join(backup, "original"))
	case "invalid-account":
		err = os.Remove(filepath.Join(account, ".claude", ".credentials.json"))
	case "foreign-unit":
		err = os.WriteFile(filepath.Join(root, "unit-state"), []byte(unitFields("active", "", "success", "0", "0")), 0600)
	case "late-unit":
		command, readErr := os.ReadFile(engine.config.SystemctlPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Setenv("MIGRATION_SHOW_COUNT", filepath.Join(root, "show-count"))
		prefix := `#!/bin/sh
if [ "$1" = show ]; then
  count=0
  if [ -f "$MIGRATION_SHOW_COUNT" ]; then count=$(cat "$MIGRATION_SHOW_COUNT"); fi
  count=$((count + 1))
  printf '%s' "$count" > "$MIGRATION_SHOW_COUNT"
  if [ "$count" -ge 5 ]; then
    printf 'LoadState=loaded\nActiveState=active\n' > "$TAKEOVER_UNIT_STATE"
  fi
fi
`
		err = os.WriteFile(engine.config.SystemctlPath, append([]byte(prefix), command...), 0700)
	case "empty-cgroup", "populated-cgroup", "linked-cgroup":
		group := filepath.Join(engine.config.CgroupRoot, "system.slice", original.Copy.Unit)
		if err := os.MkdirAll(filepath.Dir(group), 0700); err != nil {
			t.Fatal(err)
		}
		if kind == "linked-cgroup" {
			err = os.Symlink("absent", group)
			break
		}
		if err := os.Mkdir(group, 0700); err != nil {
			t.Fatal(err)
		}
		population := "populated 0\n"
		if kind == "populated-cgroup" {
			population = "populated 1\n"
		}
		err = os.WriteFile(filepath.Join(group, "cgroup.events"), []byte(population), 0600)
	case "rollback-intent":
		err = skillmanager.BeginMigrationOwnership(store, skillmanager.MigrationOwnershipIntent{Version: 1, Copy: original.Copy, Phase: "rollback", Backend: "docker_sandbox"})
	case "missing-phase":
		entries, readErr := os.ReadDir(engine.config.SkillStateRoot)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "migration-writer-") {
				err = os.Remove(filepath.Join(engine.config.SkillStateRoot, entry.Name()))
				break
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}

type migrationRecoveryFile struct {
	Mode              os.FileMode
	UID, GID          uint32
	Inode             uint64
	Size              int64
	Modified, Changed syscall.Timespec
	Digest            [32]byte
}

func migrationRecoveryInventory(t *testing.T, roots ...string) map[string]migrationRecoveryFile {
	t.Helper()
	files := make(map[string]migrationRecoveryFile)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) && path == root {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			stat := info.Sys().(*syscall.Stat_t)
			file := migrationRecoveryFile{Mode: info.Mode(), UID: stat.Uid, GID: stat.Gid, Inode: stat.Ino, Size: stat.Size, Modified: stat.Mtim, Changed: stat.Ctim}
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				file.Digest = sha256.Sum256(data)
			}
			files[path] = file
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}
