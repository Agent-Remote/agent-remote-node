package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestAccountCopySystemdBackup(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd and private cgroup namespace")
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancelled-client"}[cancelled], func(t *testing.T) {
			engine, binding, root := accountTakeoverFixture(t)
			launcherLog := filepath.Join(root, "launcher.log")
			t.Setenv("COPY_LAUNCHER_LOG", launcherLog)
			scriptRoot, err := os.MkdirTemp("/var/tmp", "account-copy-launcher-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(scriptRoot) })
			engine.config.SystemdRunPath = filepath.Join(scriptRoot, "systemd-run")
			if err := os.WriteFile(engine.config.SystemdRunPath, []byte("#!/bin/sh\n"+`exec systemd-run "$@" 2> "$COPY_LAUNCHER_LOG"`+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if t.Failed() {
					data, err := os.ReadFile(launcherLog)
					t.Logf("test launcher: %s (%v)", data, err)
				}
			})
			engine.config.SystemctlPath = "systemctl"
			engine.config.CgroupRoot = "/sys/fs/cgroup"
			suffix := binding.TaskID
			if cancelled {
				suffix = binding.TakeoverID
			}
			task := "migrate_tool_account_runtime:" + binding.AccountID + ":" + suffix
			unit := skillmanager.AccountCopyUnit(task)
			t.Cleanup(func() {
				if t.Failed() {
					state, err := readNativeUnit(context.Background(), "systemctl", unit)
					t.Logf("unit state: %#v %v", state, err)
				}
			})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = exec.CommandContext(ctx, "systemctl", "stop", unit).Run()
				_ = exec.CommandContext(ctx, "systemctl", "reset-failed", unit).Run()
			})
			source, target := filepath.Join(root, "source"), filepath.Join(root, "backup")
			for _, path := range []string{source, target, filepath.Join(source, "empty")} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "learned"), []byte("private learned content"), 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("learned", filepath.Join(source, "alias")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if cancelled {
				ready, release := filepath.Join(root, "ready"), filepath.Join(root, "release")
				t.Setenv("COPY_READY", ready)
				t.Setenv("COPY_RELEASE", release)
				scriptRoot, err := os.MkdirTemp("/var/tmp", "account-copy-proof-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(scriptRoot) })
				engine.config.SystemdRunPath = filepath.Join(scriptRoot, "delayed-copy")
				if err := os.WriteFile(engine.config.SystemdRunPath, []byte("#!/bin/sh\n"+`exec systemd-run --quiet --wait "$3" --property=KillMode=control-group --property=LimitCORE=0 /bin/sh -c 'touch "$1"; while [ ! -e "$2" ]; do sleep .05; done' copy "$COPY_READY" "$COPY_RELEASE" 2> "$COPY_LAUNCHER_LOG"`+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					done <- engine.backupMigratingAccount(ctx, task, binding.UserID, binding.AccountID, "native", "docker_sandbox", source, target)
				}()
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(ready); err == nil {
						break
					}
					select {
					case err := <-done:
						t.Fatalf("copy launcher ended before readiness: %v", err)
					default:
					}
					if time.Now().After(deadline) {
						cancel()
						<-done
						t.Fatal("copy service did not start")
					}
					time.Sleep(20 * time.Millisecond)
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, errAccountCopyPending) {
						t.Fatal("active service not retained as unknown", err)
					}
				case <-time.After(12 * time.Second):
					t.Fatal("copy client cancellation stuck")
				}
				if err := confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+unit); err == nil {
					t.Fatal("test writer did not survive its client")
				}
				if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
				deadline = time.Now().Add(5 * time.Second)
				for confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+unit) != nil {
					if time.Now().After(deadline) {
						t.Fatal("copy writer did not exit")
					}
					time.Sleep(20 * time.Millisecond)
				}
			} else {
				if err := engine.backupMigratingAccount(ctx, task, binding.UserID, binding.AccountID, "native", "docker_sandbox", source, target); err != nil {
					t.Fatal(err)
				}
				bytes, err := os.ReadFile(filepath.Join(target, "learned"))
				if err != nil || string(bytes) != "private learned content" {
					t.Fatal("copy content differs", err)
				}
				link, err := os.Readlink(filepath.Join(target, "alias"))
				if err != nil || link != "learned" {
					t.Fatal("link differs", err)
				}
				mode, err := os.Stat(filepath.Join(target, "learned"))
				if err != nil || mode.Mode().Perm() != 0750 {
					t.Fatal("mode differs", err)
				}
			}
			names, err := filepath.Glob(filepath.Join(engine.config.SkillStateRoot, "account-copy-*.json"))
			if err != nil || len(names) != 1 {
				t.Fatal("receipt missing", err)
			}
			data, err := os.ReadFile(names[0])
			if err != nil {
				t.Fatal(err)
			}
			var receipt skillmanager.AccountCopyReceipt
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			want := "copied"
			if cancelled {
				want = "started"
			}
			if receipt.State != want {
				t.Fatal("wrong copy evidence", receipt.State)
			}
			if err := engine.backupMigratingAccount(context.Background(), task, binding.UserID, binding.AccountID, "native", "docker_sandbox", source, target); !errors.Is(err, errAccountCopyPending) {
				t.Fatal("copy-only receipt allowed replay", err)
			}
		})
	}
}
