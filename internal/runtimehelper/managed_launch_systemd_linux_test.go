package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func managedSystemdLaunchFixture(t *testing.T) (Engine, ManagedSessionSpecRequest, SessionSpec, skillmanager.SessionLaunch) {
	t.Helper()
	return managedSystemdLaunchFixtureWithBrowser(t, false)
}

func managedSystemdLaunchFixtureWithBrowser(t *testing.T, browser bool) (Engine, ManagedSessionSpecRequest, SessionSpec, skillmanager.SessionLaunch) {
	t.Helper()
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd and writable private cgroups")
	}
	engine, input, spec, starting := preparedManagedLaunchWithConfig(t, func(engine *Engine, input *ManagedSessionSpecRequest) {
		root := filepath.Dir(engine.config.StateRoot)
		for _, path := range []string{root, filepath.Dir(root)} {
			if err := os.Chmod(path, 0o711); err != nil {
				t.Fatal(err)
			}
		}
		engine.config.RuntimeBinaryPath = "/proof/agent-remote-runtime"
		engine.config.ClaudeRuntimePath = filepath.Join(root, "artifact", "bin", "claude")
		engine.config.SystemctlPath, engine.config.SystemdRunPath = "systemctl", "systemd-run"
		engine.config.CgroupRoot, engine.config.IPPath, engine.config.NFTPath = "/sys/fs/cgroup", "ip", "nft"
		username := "ar-u-" + shortDigest(input.Snapshot.UserID, 12)
		if err := exec.Command("id", "-u", username).Run(); err != nil {
			for _, args := range [][]string{{"groupadd", "--gid", "12345", username}, {"useradd", "--uid", "12345", "--gid", "12345", "--no-create-home", "--shell", "/bin/sh", username}} {
				if output, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
					t.Fatalf("create proof identity: %s %v", output, err)
				}
			}
		}
		account := filepath.Join(engine.config.AccountRoot, input.Snapshot.UserID, "tool-accounts", "claude", input.Snapshot.AccountID)
		if err := engine.prepareOwnedDirectories(input.Snapshot.UserID, account, filepath.Join(account, ".claude")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(account, ".claude.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(engine.config.ClaudeRuntimePath), 0o755); err != nil {
			t.Fatal(err)
		}
		program := "#!/bin/sh\nset -eu\nprintf entered >> /account/.claude/skills/executions\ntest \"$(cat /home/runtime/.claude/skills/executions)\" = entered\nwhile ! test -f /account/.claude/skills/finish; do sleep 0.05; done\nexit 0\n"
		if err := os.WriteFile(engine.config.ClaudeRuntimePath, []byte(program), 0o755); err != nil {
			t.Fatal(err)
		}
		if browser {
			enableManagedTestBrowser(t, engine, input)
		}
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "systemctl", "stop", spec.UnitName).Run()
		_ = exec.CommandContext(ctx, "systemctl", "reset-failed", spec.UnitName).Run()
		_ = engine.cleanupTemp(ctx, spec)
		_ = engine.cleanupNativeSkillMount(spec)
		_ = exec.CommandContext(ctx, "ip", "netns", "delete", spec.NetworkNamespace).Run()
	})
	return engine, input, spec, starting
}

func TestManagedLaunchSystemdRecoveryDoesNotExecuteTwice(t *testing.T) {
	engine, input, spec, starting := managedSystemdLaunchFixture(t)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := client.StartManagedSession(ctx, "original_managed_task", input)
	if err != nil {
		log, _ := exec.Command("journalctl", "-u", spec.UnitName, "--no-pager", "-n", "20", "-o", "cat").CombinedOutput()
		t.Fatalf("managed real launch failed: %v\n%s", err, log)
	}
	if result["status"] != "running" {
		t.Fatal("managed runtime not ready", result)
	}
	original, err := engine.managedLaunchInvocation(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.ReconcileSkillSession(ctx, "inspect-original-runtime", input.Snapshot.NodeID, spec.SessionID)
	if err != nil || observation.State != "running" || observation.Record != nil {
		t.Fatal("background reconciliation disturbed running original runtime", observation, err)
	}
	verifyManagedRecoveryRequiresSavedInvocation(t, engine, input, spec, client)
	digest := sha256.Sum256([]byte(spec.TmuxSessionName))
	if _, err := nativeTmuxCommand(ctx, engine.config, spec, "wait-for", "-S", fmt.Sprintf("agent-remote-client-%x", digest[:8])); err != nil {
		t.Fatal(err)
	}
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	for {
		if data, err := bundle.ReadFile("work/executions"); err == nil && string(data) == "entered" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("tool did not execute", ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	// Fault injection recreates a crash after readiness but before sealing the launch receipt.
	data, err := json.Marshal(starting)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Recovery must reject a weakened mount without silently repairing a running runtime.
	target := filepath.Join(spec.SessionRoot, "skill-work")
	if err := unix.Mount("", target, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_NODEV, ""); err != nil {
		t.Fatal(err)
	}
	_, err = client.StartManagedSession(ctx, "original_managed_task", input)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "SKILL_START_PENDING" {
		t.Fatal("recovery accepted a weakened mount", err)
	}
	var flags unix.Statfs_t
	if err := unix.Statfs(target, &flags); err != nil || flags.Flags&unix.ST_NOSUID != 0 {
		t.Fatal("recovery repaired the live mount", err)
	}
	if err := hardenSkillMount(target); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.RecoverManagedSession(ctx, "original_managed_task", input); err != nil {
			t.Fatal("live recovery failed", err)
		}
		invocation, err := engine.managedLaunchInvocation(ctx, spec)
		if err != nil || invocation != original {
			t.Fatal("recovery replaced systemd invocation", err)
		}
		if content, err := bundle.ReadFile("work/executions"); err != nil || string(content) != "entered" {
			t.Fatal("recovery executed tool twice", err)
		}
	}
	file, err := bundle.OpenFile("work/finish", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	for {
		active, err := engine.nativeSessionActive(spec)
		if err == nil && !active {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("tool did not exit", ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
		t.Fatal(err)
	}
	observation, err = client.ReconcileSkillSession(ctx, "observe-natural-exit", input.Snapshot.NodeID, spec.SessionID)
	if err != nil || observation.State != "finalized" || observation.Record == nil || observation.Record.Unclean || observation.Record.State != "local_durable" {
		t.Fatal("natural exit not retained cleanly", observation, err)
	}
	acknowledged, err := client.AcknowledgeSkillFinalization(ctx, "natural-exit-ack", helperFinalizationAck(*observation.Record, "published"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CleanupFinalizedSkillSession(ctx, "natural-exit-cleanup", acknowledged); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartManagedSession(ctx, "original_managed_task", input); err != nil {
		t.Fatal("historical readiness receipt lost after cleanup", err)
	}
	_, err = client.RecoverManagedSession(ctx, "original_managed_task", input)
	if !errors.As(err, &failure) || failure.Code != "SKILL_START_STOPPED" {
		t.Fatal("historical readiness was mistaken for a live runtime", err)
	}
	if _, err := os.Lstat(spec.SessionRoot); !os.IsNotExist(err) {
		t.Fatal("historical replay recreated transient runtime", err)
	}
	if state, err := readNativeUnit(ctx, engine.config.SystemctlPath, spec.UnitName); err != nil || !strings.Contains("inactive failed", state.ActiveState) {
		t.Fatal("historical replay relaunched process", err)
	}
}

func verifyManagedRecoveryRequiresSavedInvocation(t *testing.T, engine Engine, input ManagedSessionSpecRequest, spec SessionSpec, client Client) {
	t.Helper()
	path := filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record skillmanager.SessionLaunch
	if err := json.Unmarshal(original, &record); err != nil {
		t.Fatal(err)
	}
	if record.InvocationID == strings.Repeat("a", 32) {
		record.InvocationID = strings.Repeat("b", 32)
	} else {
		record.InvocationID = strings.Repeat("a", 32)
	}
	changed, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = client.RecoverManagedSession(context.Background(), "original_managed_task", input)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "SKILL_START_PENDING" {
		t.Fatal("recovery adopted another invocation", err)
	}
	if err := client.CancelManagedSession(context.Background(), "original_managed_task", input); !errors.As(err, &failure) || failure.Code != "SKILL_START_PENDING" {
		t.Fatal("cancellation adopted another invocation", err)
	}
	if active, err := engine.nativeSessionActive(spec); err != nil || !active {
		t.Fatal("uncertain invocation recovery stopped active service", err)
	}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestManagedLaunchSystemdCancellationRetainsWork(t *testing.T) {
	engine, input, spec, launch := managedSystemdLaunchFixture(t)
	server := NewServer("", -1, os.Geteuid(), engine)
	done := make(chan struct{}, 2)
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
		server.handle(ctx, connection)
		done <- struct{}{}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := client.StartManagedSession(ctx, "original_managed_task", input); result <- err }()
	watch, cancelWatch := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelWatch()
	for {
		state, err := readNativeUnit(watch, engine.config.SystemctlPath, spec.UnitName)
		if err == nil && state.LoadState == "loaded" && state.ActiveState == "active" {
			break
		}
		select {
		case <-watch.Done():
			t.Fatal("launch did not create a unit", watch.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("client lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client kept blocked after cancellation")
	}
	select {
	case <-done:
	case <-time.After(40 * time.Second):
		t.Fatal("cancelled Helper did not finish writer recovery")
	}
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := skillmanager.ReadSessionLaunch(store, launch)
	if err != nil || saved.State != "starting" && saved.State != "observed" {
		t.Fatal("cancelled start became ready", err)
	}
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	journal, err := skillmanager.ReadFinalization(bundle, receipt.Snapshot.Binding)
	if err != nil || !journal.Unclean {
		t.Fatal("cancelled launch lost unclean finalization", err)
	}
	if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "original" {
		t.Fatal("cancelled launch lost original work", err)
	}
	_, err = client.StartManagedSession(context.Background(), "original_managed_task", input)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "SKILL_START_STOPPED" {
		t.Fatal("cancelled start was replayed", err)
	}
	if active, err := engine.nativeSessionActive(spec); err != nil || active {
		t.Fatal("cancelled launch still has writers", err)
	}
}

func TestManagedLaunchSystemdExplicitCancellationRetainsWork(t *testing.T) {
	engine, input, spec, _ := managedSystemdLaunchFixture(t)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := client.StartManagedSession(ctx, "original_managed_task", input); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Session.Argv = []string{"replacement"}
	if err := client.CancelManagedSession(ctx, "original_managed_task", changed); err == nil {
		t.Fatal("foreign input cancelled the original runtime")
	}
	if active, err := engine.nativeSessionActive(spec); err != nil || !active {
		t.Fatal("foreign cancellation stopped original runtime", err)
	}
	for range 2 {
		if err := client.CancelManagedSession(ctx, "original_managed_task", input); err != nil {
			t.Fatal("original cancellation failed", err)
		}
	}
	if active, err := engine.nativeSessionActive(spec); err != nil || active {
		t.Fatal("explicit cancellation left active writers", err)
	}
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if _, err := skillmanager.ReadFinalization(bundle, receipt.Snapshot.Binding); err != nil {
		t.Fatal("explicit cancellation lost finalization", err)
	}
	if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "original" {
		t.Fatal("explicit cancellation lost work", err)
	}
}
