package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const kernelRebootReceipt = "/var/lib/ar-kernel-reboot.json"

type kernelRebootInput struct {
	Config EngineConfig              `json:"config"`
	Input  ManagedSessionSpecRequest `json:"input"`
	Spec   SessionSpec               `json:"spec"`
}

// This test is driven across two real guest kernels by linux_skill_kernel_reboot_test.sh.
// The seed phase intentionally never returns: the external VM owner cuts power after fsync.
func TestManagedSkillActualKernelReboot(t *testing.T) {
	phase := os.Getenv("AGENT_REMOTE_SKILL_KERNEL_REBOOT_PHASE")
	if phase == "" {
		t.Skip("requires disposable two-boot VM")
	}
	if phase == "seed" {
		seedKernelReboot(t)
		return
	}
	if phase != "recover" {
		t.Fatal("unknown kernel reboot phase")
	}
	data, err := os.ReadFile(kernelRebootReceipt)
	if err != nil {
		t.Fatal(err)
	}
	var saved kernelRebootInput
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == saved.Spec.BootID {
		t.Fatal("kernel did not actually change", err)
	}
	engine := NewEngine(saved.Config)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	observed, err := client.ReconcileSkillSession(ctx, "kernel-reboot-reconcile", saved.Input.Snapshot.NodeID, saved.Spec.SessionID)
	if err != nil || observed.Record == nil || !observed.Record.Unclean || observed.Record.State != "local_durable" || observed.Record.ObjectsVersion != 1 {
		t.Fatal("actual reboot did not retain unclean capture", observed, err)
	}
	bundle, _, err := engine.retainedNativeSkillSession(saved.Spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if content, err := bundle.ReadFile("work/executions"); err != nil || string(content) != "entered" {
		t.Fatal("reboot lost writes or replayed tool", err)
	}
	if _, err := client.CleanupFinalizedSkillSession(ctx, "kernel-reboot-premature-cleanup", *observed.Record); err == nil {
		t.Fatal("local durability authorized cleanup")
	}
	ack, err := client.AcknowledgeSkillFinalization(ctx, "kernel-reboot-ack", helperFinalizationAck(*observed.Record, "detached"))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.CleanupFinalizedSkillSession(ctx, "kernel-reboot-cleanup", ack); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := client.ReconcileSkillSession(ctx, "kernel-reboot-replay", saved.Input.Snapshot.NodeID, saved.Spec.SessionID)
	if err != nil || replay.Record == nil || *replay.Record != ack {
		t.Fatal("cleanup lost original retained operation", err)
	}
	if _, err := os.Stat(saved.Spec.SessionRoot); !os.IsNotExist(err) {
		t.Fatal("old transient resources remain", err)
	}
	if content, err := bundle.ReadFile("work/executions"); err != nil || string(content) != "entered" {
		t.Fatal("cleanup removed original work", err)
	}
	fmt.Printf("AR_KERNEL_REBOOT_RECOVERED old_boot=%s new_boot=%s state=%s\n", saved.Spec.BootID, strings.TrimSpace(string(boot)), ack.State)
}

func seedKernelReboot(t *testing.T) {
	t.Helper()
	engine, input, spec, _ := managedSystemdLaunchFixture(t)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := client.StartManagedSession(ctx, "original_managed_task", input); err != nil {
		log, _ := exec.Command("journalctl", "-u", spec.UnitName, "--no-pager", "-n", "30", "-o", "cat").CombinedOutput()
		t.Fatalf("seed runtime failed: %v\n%s", err, log)
	}
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
		if content, err := bundle.ReadFile("work/executions"); err == nil && string(content) == "entered" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("seed tool did not write", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	if active, err := engine.nativeSessionActive(spec); err != nil || !active {
		t.Fatal("seed process is not running", err)
	}
	data, err := json.Marshal(kernelRebootInput{Config: engine.config, Input: input, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kernelRebootReceipt, data, 0600); err != nil {
		t.Fatal(err)
	}
	// The guest's known durable writes precede the cut; no shutdown hook or finalizer can run.
	unix.Sync()
	fmt.Printf("AR_KERNEL_REBOOT_SEEDED boot=%s root=%s\n", spec.BootID, filepath.Dir(engine.config.StateRoot))
	<-time.After(4 * time.Minute)
	t.Fatal("VM power cut was not performed")
}
