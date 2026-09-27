package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestAccountMigrationSystemdLifecycle(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd and root identities")
	}
	t.Setenv("TMPDIR", "/var/tmp")
	for index, kind := range []string{"success", "failed-target-acl", "failed-rollback-access", "failed-rollback-default", "failed-rollback-traverse", "unknown-acl", "failed-rollback-identity", "failed-target-acl-custom-source", "failed-target-acl-residual"} {
		t.Run(kind, func(t *testing.T) {
			terminal := kind == "success" || strings.HasPrefix(kind, "failed-target-acl")
			var expected error
			if strings.HasPrefix(kind, "failed-target-acl") {
				expected = errMigrationFailed
			} else if !terminal {
				expected = errMigrationWritersUnknown
			}
			engine, binding, _ := accountTakeoverFixture(t)
			// Keep the real account separate from Native lifecycle fixtures with fixed numeric IDs.
			binding.UserID = "99999999-9999-4999-8999-999999999999"
			engine.config.SystemdRunPath = "systemd-run"
			engine.config.SystemctlPath = "systemctl"
			engine.config.CgroupRoot = "/sys/fs/cgroup"
			engine.config.SetfaclPath = "/usr/bin/setfacl"
			task := "migrate_tool_account_runtime:" + binding.AccountID + ":77777777-7777-4777-8777-" + strings.Repeat("0", 11) + strconv.Itoa(index)
			account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
			backup := filepath.Join(engine.config.StateRoot, "migrations", shortDigest(task, 32))
			if err := os.MkdirAll(filepath.Join(account, ".claude", "skills"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(account, ".claude", ".credentials.json"), []byte(`{"fixture":true}`), 0600); err != nil {
				t.Fatal(err)
			}
			learned := filepath.Join(account, ".claude", "skills", "memory")
			if err := os.WriteFile(learned, []byte("original learned content"), 0640); err != nil {
				t.Fatal(err)
			}
			units := []string{skillmanager.AccountCopyUnit(task)}
			for _, phase := range []string{"target", "rollback"} {
				for i := 0; i < 3; i++ {
					units = append(units, "agent-remote-own-"+shortDigest(task+":"+phase+":"+strconv.Itoa(i), 32)+".service")
				}
			}
			t.Cleanup(func() {
				for _, unit := range units {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = exec.CommandContext(ctx, "systemctl", "stop", unit).Run()
					_ = exec.CommandContext(ctx, "systemctl", "reset-failed", unit).Run()
					cancel()
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if kind == "failed-target-acl" || kind == "failed-target-acl-residual" {
				prepareMigrationSourcePermissions(t, engine, account)
			}
			if kind == "failed-rollback-identity" {
				// Target ACLs can name root, but root is never a valid source runtime identity.
				engine.config.NodeUser = "root"
				if err := os.Remove(filepath.Join(account, ".claude", ".credentials.json")); err != nil {
					t.Fatal(err)
				}
			} else if strings.HasPrefix(kind, "failed-") {
				engine.config.SetfaclPath = migrationACLFailureCommand(t, kind)
			}
			if kind == "unknown-acl" {
				scripts, err := os.MkdirTemp("/var/tmp", "migration-acl-proof-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(scripts) })
				ready, release := filepath.Join(scripts, "ready"), filepath.Join(scripts, "release")
				engine.config.SetfaclPath = filepath.Join(scripts, "setfacl")
				script := "#!/bin/sh\ntouch '" + ready + "'\nwhile [ ! -e '" + release + "' ]; do sleep .05; done\n"
				if err := os.WriteFile(engine.config.SetfaclPath, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					done <- engine.executeAccountMigration(ctx, task, binding.UserID, binding.AccountID, "docker_sandbox", "native", account, backup)
				}()
				deadline := time.Now().Add(8 * time.Second)
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					select {
					case err := <-done:
						t.Fatal("migration exited before ACL", err)
					default:
					}
					if time.Now().After(deadline) {
						cancel()
						<-done
						t.Fatal("ACL service did not start")
					}
					time.Sleep(20 * time.Millisecond)
				}
				cancel()
				if err := <-done; !errors.Is(err, errMigrationWritersUnknown) {
					t.Fatal("unknown ACL did not remain pending", err)
				}
				state, err := readNativeUnit(context.Background(), "systemctl", units[1])
				if err != nil || state.ActiveState != "active" {
					t.Fatal("writer did not survive launcher", state, err)
				}
				if err := NewEngine(engine.config).requireLegacyAccountRuntime(binding.UserID, binding.AccountID); !errors.Is(err, errMigrationWritersUnknown) {
					t.Fatal("live migration writer did not prevent legacy admission", err)
				}
				if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
				deadline = time.Now().Add(5 * time.Second)
				for confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+units[1]) != nil {
					if time.Now().After(deadline) {
						t.Fatal("ACL service did not finish")
					}
					time.Sleep(20 * time.Millisecond)
				}
			} else {
				_, err := engine.Execute(ctx, Request{
					Version: ProtocolVersion, RequestID: task, Operation: "migrate_account",
					Payload: map[string]any{"tool_account_id": binding.AccountID, "user_id": binding.UserID, "tool_type": "claude", "source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native"},
				})
				if !errors.Is(err, expected) {
					for _, unit := range units {
						state, err := readNativeUnit(context.Background(), "systemctl", unit)
						t.Logf("unit %s: %+v %v", unit, state, err)
					}
					state, inspectionErr := readNativeUnit(context.Background(), "systemctl", units[0])
					t.Fatalf("%s: %v; copy state=%+v inspection=%v cgroup=%v", kind, err, state, inspectionErr, confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+units[0]))
				}
			}
			store, err := engine.openSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			proof := skillmanager.CheckAccountMigrationWriter(store, binding.NodeID, binding.UserID, binding.AccountID, task)
			if (proof == nil) != terminal {
				t.Fatal("wrong writer proof", kind, proof)
			}
			recovery := explicitRecoveryRequest(Request{RequestID: task, Payload: map[string]any{
				"tool_account_id": binding.AccountID, "user_id": binding.UserID, "tool_type": "claude",
				"source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native",
			}}, binding.NodeID)
			recovery.Payload["binding"].(map[string]any)["version"] = 2
			recovery.Payload["binding"].(map[string]any)["action"] = "verify_source"
			beforeVerification := migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)
			result, verifyErr := NewEngine(engine.config).Execute(context.Background(), recovery)
			if strings.HasPrefix(kind, "failed-target-acl") {
				if verifyErr != nil || result["recovered"] != true {
					t.Fatal("restored source verification rejected", verifyErr)
				}
			} else if verifyErr == nil {
				t.Fatal("source verification admitted absent rollback evidence")
			}
			if !reflect.DeepEqual(beforeVerification, migrationRecoveryInventory(t, engine.config.SkillStateRoot, account, backup)) {
				t.Fatal("source verification changed original evidence or content")
			}
			if kind == "success" {
				verifyAttestedMigrationRecovery(t, engine, store, task, binding.UserID, binding.AccountID, account, backup)
			}
			if kind == "failed-target-acl" {
				assertMigrationSourceReadable(t, engine, learned)
			}
			if err := os.WriteFile(learned, []byte("after original migration"), 0640); err != nil {
				t.Fatal(err)
			}
			if _, err := NewEngine(engine.config).Execute(context.Background(), recovery); err == nil {
				t.Fatal("source verification ignored changed original content")
			}
			replay := engine.executeAccountMigration(context.Background(), task, binding.UserID, binding.AccountID, "docker_sandbox", "native", account, backup)
			if !errors.Is(replay, expected) {
				t.Fatal("replay changed outcome", replay)
			}
			copied, err := os.ReadFile(filepath.Join(backup, ".claude", "skills", "memory"))
			if err != nil || string(copied) != "original learned content" {
				t.Fatal("replay overwrote backup", err)
			}
			if !terminal {
				replacement := "migrate_tool_account_runtime:" + binding.AccountID + ":88888888-8888-4888-8888-888888888888"
				if err := engine.executeAccountMigration(context.Background(), replacement, binding.UserID, binding.AccountID, "docker_sandbox", "native", account, backup+"-replacement"); !errors.Is(err, errMigrationWritersUnknown) {
					t.Fatal("new task bypassed unresolved writer", err)
				}
				if _, err := os.Stat(backup + "-replacement"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("replacement mutated before admission")
				}
			}
		})
	}
}
