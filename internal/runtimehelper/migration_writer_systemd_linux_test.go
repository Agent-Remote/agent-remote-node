package runtimehelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestMigrationWriterSystemdReplacementPreservesOriginalInvocation(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd and root identities")
	}
	t.Setenv("TMPDIR", "/var/tmp")
	engine, binding, root := accountTakeoverFixture(t)
	engine.config.SystemdRunPath, engine.config.SystemctlPath, engine.config.CgroupRoot = "systemd-run", "systemctl", "/sys/fs/cgroup"
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task := "migrate_tool_account_runtime:" + binding.AccountID + ":cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	copy := skillmanager.AccountCopyReceipt{Version: 1, WriterVersion: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, TaskID: task, InputDigest: strings.Repeat("a", 64), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
	if err := skillmanager.BeginAccountCopy(store, copy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "systemctl", "stop", copy.Unit).Run()
		_ = exec.CommandContext(ctx, "systemctl", "reset-failed", copy.Unit).Run()
	})
	script := filepath.Join(root, "writer")
	ready, release, count := filepath.Join(root, "ready"), filepath.Join(root, "release"), filepath.Join(root, "count")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nset -eu\nprintf x >> \"$3\"\ntouch \"$1\"\nwhile [ ! -e \"$2\" ]; do sleep .05; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	arguments := []string{ready, release, count}
	identity := skillmanager.MigrationWriterIdentity{Copy: copy, Phase: "copy", CommandDigest: migrationDigest(append([]string{engine.config.SystemdRunPath, engine.config.SystemctlPath, engine.config.CgroupRoot, script}, arguments...))}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make(chan bool, 1)
	go func() {
		_, drained := engine.runMigrationWriter(ctx, copy, "copy", script, arguments...)
		result <- drained
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !pathExists(ready) {
		if time.Now().After(deadline) {
			cancel()
			<-result
			t.Fatal("original writer did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if drained := <-result; drained {
		t.Fatal("cancelled active writer became terminal")
	}
	saved, err := skillmanager.ReadMigrationWriter(store, identity)
	if err != nil || saved.State != "observed" || !validManagedInvocationID(saved.InvocationID) {
		t.Fatal("cancelled launch lost invocation", err)
	}
	replacement := NewEngine(engine.config)
	observed, err := replacement.observeMigrationWriter(context.Background(), store, saved)
	if err != nil || observed != saved {
		t.Fatal("replacement changed original live authority", err)
	}
	if _, drained := replacement.runMigrationWriter(context.Background(), copy, "copy", script, arguments...); drained {
		t.Fatal("retained writer relaunched")
	}
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		observed, err = replacement.observeMigrationWriter(context.Background(), store, saved)
		if err != nil {
			t.Fatal(err)
		}
		if observed.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("original invocation did not converge after exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if observed.InvocationID != saved.InvocationID {
		t.Fatal("completion changed invocation")
	}
	data, err := os.ReadFile(count)
	if err != nil || string(data) != "x" {
		t.Fatal("writer executed more than once", err)
	}
	replacement.cleanupMigrationWriter(observed)
	foreignCtx, foreignCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer foreignCancel()
	command := exec.CommandContext(foreignCtx, "systemd-run", "--quiet", "--unit="+copy.Unit, "--description=foreign fixture", "/bin/sleep", "60")
	if err := command.Run(); err != nil {
		t.Fatal("cannot create distinct owned replacement fixture", err)
	}
	replacement.cleanupMigrationWriter(observed)
	current, err := readManagedNativeUnit(foreignCtx, "systemctl", copy.Unit)
	if err != nil || current.ActiveState != "active" || current.InvocationID == saved.InvocationID {
		t.Fatal("historical cleanup stopped a replacement service", err)
	}
	retained, err := replacement.observeMigrationWriter(foreignCtx, store, observed)
	if err != nil || retained != observed {
		t.Fatal("historical completion changed", err)
	}
}
