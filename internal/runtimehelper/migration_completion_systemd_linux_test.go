package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type migrationCompletionFixture struct {
	engine   Engine
	request  Request
	original skillmanager.AccountMigrationReceipt
	account  string
	backup   string
	launches string
	root     string
}

func newMigrationCompletionFixture(t *testing.T, index int) migrationCompletionFixture {
	t.Helper()
	engine, binding, root := accountTakeoverFixture(t)
	binding.UserID = "99999999-9999-4999-8999-999999999999"
	engine.config.SystemctlPath, engine.config.CgroupRoot, engine.config.SetfaclPath = "systemctl", "/sys/fs/cgroup", "/usr/bin/setfacl"
	launches := filepath.Join(root, "launches")
	engine.config.SystemdRunPath = filepath.Join(root, "systemd-run")
	if err := os.WriteFile(engine.config.SystemdRunPath, []byte("#!/bin/sh\nprintf x >> '"+launches+"'\nexec /usr/bin/systemd-run \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	task := "migrate_tool_account_runtime:" + binding.AccountID + ":dddddddd-dddd-4ddd-8ddd-" + strings.Repeat("0", 11) + strconv.Itoa(index)
	account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
	backup := filepath.Join(engine.config.StateRoot, "migrations", shortDigest(task, 32))
	if err := os.MkdirAll(filepath.Join(account, ".claude", "skills"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, ".claude", ".credentials.json"), []byte(`{"fixture":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, ".claude", "skills", "memory"), []byte("original learning"), 0640); err != nil {
		t.Fatal(err)
	}
	copy := skillmanager.AccountCopyReceipt{Version: 1, WriterVersion: 2, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, TaskID: task, InputDigest: migrationDigest([]string{"docker_sandbox", "native", account, backup}), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
	request := Request{Version: ProtocolVersion, RequestID: task, Operation: "migrate_account", Payload: map[string]any{"tool_account_id": binding.AccountID, "user_id": binding.UserID, "tool_type": "claude", "source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native"}}
	fixture := migrationCompletionFixture{engine: engine, request: request, account: account, backup: backup, launches: launches, root: root, original: skillmanager.AccountMigrationReceipt{Version: 1, Copy: copy, State: "started"}}
	t.Cleanup(func() {
		for _, phase := range migrationPhases {
			unit, err := skillmanager.MigrationWriterUnit(skillmanager.MigrationWriterIdentity{Copy: copy, Phase: phase, CommandDigest: strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = exec.CommandContext(ctx, "systemctl", "stop", unit).Run()
			_ = exec.CommandContext(ctx, "systemctl", "reset-failed", unit).Run()
			cancel()
		}
	})
	return fixture
}

func TestMigrationCompletionSystemdCrashPrefixConvergesWithoutNewWriters(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd and root identities")
	}
	t.Setenv("TMPDIR", "/var/tmp")
	for index, kind := range []string{"target", "rollback", "copy-receipt-gap", "last-phase-live", "rollback-before-acl"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newMigrationCompletionFixture(t, index)
			engine, original := fixture.engine, fixture.original
			if kind == "rollback-before-acl" {
				engine.config.SetfaclPath = filepath.Join(fixture.root, "setfacl")
				if err := os.WriteFile(engine.config.SetfaclPath, []byte("#!/bin/sh\nexec /usr/bin/setfacl \"$@\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			var ready, release string
			if kind == "last-phase-live" {
				ready, release = filepath.Join(fixture.root, "ready"), filepath.Join(fixture.root, "release")
				engine.config.SetfaclPath = delayedFinalMigrationACL(t, fixture.root, ready, release)
			}
			original.InputDigest = migrationDigest([]string{original.Copy.InputDigest, engine.config.NodeUser, engine.config.SetfaclPath, engine.config.SystemdRunPath, engine.config.SystemctlPath, engine.config.CgroupRoot})
			store, err := engine.openSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := skillmanager.BeginAccountMigration(store, original); err != nil {
				t.Fatal(err)
			}
			if err := ensureRootDirectory(fixture.backup, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if kind == "copy-receipt-gap" {
				if err := skillmanager.BeginAccountCopy(store, original.Copy); err != nil {
					t.Fatal(err)
				}
				copied, drained := engine.runAccountCopy(ctx, original.Copy, fixture.account, fixture.backup)
				if !copied || !drained {
					t.Fatal("original real copy did not complete")
				}
			} else {
				if err := engine.backupMigratingAccount(ctx, original.Copy.TaskID, original.Copy.UserID, original.Copy.AccountID, "docker_sandbox", "native", fixture.account, fixture.backup); err != nil {
					t.Fatal(err)
				}
				if kind == "last-phase-live" {
					done := make(chan error, 1)
					go func() {
						done <- engine.applyMigrationOwnership(ctx, original.Copy, "target", original.Copy.UserID, fixture.account, "native")
					}()
					deadline := time.Now().Add(5 * time.Second)
					for !pathExists(ready) {
						if time.Now().After(deadline) {
							cancel()
							<-done
							t.Fatal("last ACL did not start")
						}
						time.Sleep(20 * time.Millisecond)
					}
					cancel()
					if err := <-done; !errors.Is(err, errMigrationWritersUnknown) {
						t.Fatal("cancelled ACL did not stay pending", err)
					}
					if _, err := NewEngine(engine.config).Execute(context.Background(), fixture.request); !errors.Is(err, errMigrationWritersUnknown) {
						t.Fatal("live final phase became success", err)
					}
					if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := engine.applyMigrationOwnership(ctx, original.Copy, "target", original.Copy.UserID, fixture.account, "native"); err != nil {
						t.Fatal(err)
					}
					if kind == "rollback-before-acl" {
						if err := os.Remove(engine.config.SetfaclPath); err != nil {
							t.Fatal(err)
						}
						if err := engine.applyMigrationOwnership(ctx, original.Copy, "rollback", original.Copy.UserID, fixture.account, "docker_sandbox"); err == nil {
							t.Fatal("rollback fixture should stop before its first ACL")
						}
					}
					if kind == "rollback" {
						if err := engine.applyMigrationOwnership(ctx, original.Copy, "rollback", original.Copy.UserID, fixture.account, "docker_sandbox"); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			// The explicit operation must converge real exited original services as well.
			recoveryRequest := explicitRecoveryRequest(fixture.request, engine.config.NodeID)
			before, err := os.ReadFile(fixture.launches)
			if err != nil {
				t.Fatal(err)
			}
			recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer recoveryCancel()
			var result map[string]any
			for {
				result, err = NewEngine(engine.config).Execute(recoveryCtx, recoveryRequest)
				if kind != "last-phase-live" || !errors.Is(err, errMigrationWritersUnknown) {
					break
				}
				if recoveryCtx.Err() != nil {
					t.Fatal("exited final ACL never converged")
				}
				time.Sleep(20 * time.Millisecond)
			}
			want := "succeeded"
			if kind == "rollback" {
				want = "failed"
				if !errors.Is(err, errMigrationFailed) {
					t.Fatal("rollback lost its original failure", err)
				}
			} else if kind == "copy-receipt-gap" || kind == "rollback-before-acl" {
				want = "started"
				if !errors.Is(err, errMigrationWritersUnknown) {
					t.Fatal("missing ownership phases were bypassed", err)
				}
			} else if err != nil || result["recovered"] != true {
				t.Fatal("finished original target failed to converge", err)
			}
			if want == "succeeded" {
				// Rechecking a completed whole receipt must still inspect original service evidence.
				if _, err := NewEngine(engine.config).Execute(recoveryCtx, recoveryRequest); err != nil {
					t.Fatal("completed explicit replay failed", err)
				}
			}
			saved, err := skillmanager.ReadAccountMigration(store, original)
			if err != nil || saved.State != want {
				t.Fatal("wrong final migration metadata", err)
			}
			copied, err := skillmanager.ReadAccountCopy(store, original.Copy)
			if err != nil || copied.State != "copied" {
				t.Fatal("finished copy not recorded", err)
			}
			after, err := os.ReadFile(fixture.launches)
			if err != nil || string(before) != string(after) {
				t.Fatal("recovery launched new work", err)
			}
			data, err := os.ReadFile(filepath.Join(fixture.backup, ".claude", "skills", "memory"))
			if err != nil || string(data) != "original learning" {
				t.Fatal("recovery lost original backup", err)
			}
		})
	}
}

func delayedFinalMigrationACL(t *testing.T, root, ready, release string) string {
	t.Helper()
	command, count := filepath.Join(root, "setfacl"), filepath.Join(root, "acl-count")
	script := "#!/bin/sh\nset -eu\ncount=0\nif [ -f '" + count + "' ]; then count=$(cat '" + count + "'); fi\ncount=$((count + 1))\nprintf '%s\\n' \"$count\" > '" + count + "'\n/usr/bin/setfacl \"$@\"\nif [ \"$count\" -eq 3 ]; then touch '" + ready + "'; while [ ! -e '" + release + "' ]; do sleep .05; done; fi\n"
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return command
}
