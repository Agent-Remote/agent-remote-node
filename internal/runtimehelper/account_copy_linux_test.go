package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestAccountCopyRecordsBeforeLaunchAndNeverRepeatsUnknownWork(t *testing.T) {
	for _, kind := range []string{"success", "failed", "unknown", "active", "populated", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			engine, binding, root := accountTakeoverFixture(t)
			task := "migrate_tool_account_runtime:" + binding.AccountID + ":" + binding.TaskID
			unit := skillmanager.AccountCopyUnit(task)
			calls := filepath.Join(root, "copy-calls")
			t.Setenv("COPY_CALLS", calls)
			t.Setenv("COPY_STORE", engine.config.SkillStateRoot)
			engine.config.SystemdRunPath = writeTestCommand(t, "systemd-run", `
 test -f "$COPY_STORE"/account-copy-*.json || exit 90
 test -f "$COPY_STORE"/migration-writer-*.json || exit 91
 printf '%s\n' "$@" >> "$COPY_CALLS"
 for arg do case "$arg" in --description=*) printf 'Description=%s\n' "${arg#--description=}" >> "$TAKEOVER_UNIT_STATE";; esac; done
 `)
			state := "LoadState=loaded\nActiveState=inactive\nSubState=dead\nControlGroup=\nResult=success\nExecMainCode=1\nExecMainStatus=0\n"
			if kind == "failed" || kind == "unknown" {
				state = strings.ReplaceAll(state, "Result=success", "Result=exit-code")
				state = strings.ReplaceAll(state, "ExecMainStatus=0", "ExecMainStatus=1")
			}
			if kind == "unknown" {
				state = "LoadState=not-found\nActiveState=inactive\n"
			}
			if kind == "active" {
				state = strings.ReplaceAll(state, "ActiveState=inactive", "ActiveState=active")
			}
			state += "InvocationID=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nTransient=yes\nUser=root\nRestart=no\nRemainAfterExit=yes\n"
			if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte(state), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "populated" {
				p := filepath.Join(engine.config.CgroupRoot, "system.slice", unit)
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p, "cgroup.events"), []byte("populated 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			run := func() error {
				return engine.backupMigratingAccount(ctx, task, binding.UserID, binding.AccountID, "native", "docker_sandbox", filepath.Join(root, "source"), filepath.Join(root, "backup"))
			}
			err := run()
			if kind == "success" && err != nil {
				t.Fatal(err)
			}
			if kind != "success" && err == nil {
				t.Fatal("unproven copy succeeded")
			}
			if kind == "cancelled" {
				if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("cancelled copy launched")
				}
				return
			}
			first, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(); err == nil {
				t.Fatal("replayed ownership workflow from copy-only proof")
			}
			second, _ := os.ReadFile(calls)
			if string(first) != string(second) {
				t.Fatal("copy relaunched")
			}
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, nil); !errors.Is(err, errTakeoverWritersUnknown) {
				t.Fatal("unlisted local migration history was ignored", err)
			}
			// A backend history remains unknown even after a verified copy phase: later ACL writers are separate.
			writers := []skillmanager.AccountWriter{{Kind: "backend", NodeID: binding.NodeID, ResourceID: task, TaskID: &binding.TaskID}}
			binding.InventoryDigest, err = skillmanager.AccountInventoryDigest(writers)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, writers); !errors.Is(err, errTakeoverWritersUnknown) {
				t.Fatal("copy receipt authorized full takeover", err)
			}
		})
	}
}
