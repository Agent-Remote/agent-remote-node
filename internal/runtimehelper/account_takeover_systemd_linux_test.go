package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestNativeTakeoverSystemdDescendantQuiescence(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd PID 1 and private writable cgroup namespace")
	}
	engine, binding, _ := accountTakeoverFixture(t)
	engine.config.SystemctlPath, engine.config.CgroupRoot = "systemctl", "/sys/fs/cgroup"
	source := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID, ".claude", "skills")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(source, "learned")
	if err := os.WriteFile(original, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	control := t.TempDir()
	ready, finish := filepath.Join(control, "ready"), filepath.Join(control, "finish")
	unit := "agent-remote-session-eeeeeeeeeeee.service"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() {
		_ = os.WriteFile(finish, []byte("release"), 0o600)
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "systemctl", "stop", unit).Run()
	})
	// The main process exits while its child retains the account write handle and cgroup.
	program := `(printf ready > "$2"; while [ ! -e "$3" ]; do sleep 0.05; done; printf late-learning > "$1") & exit 0`
	if output, err := exec.CommandContext(ctx, "systemd-run", "--unit="+unit,
		"--property=KillMode=process", "--property=RemainAfterExit=yes",
		"/bin/sh", "-c", program, "takeover-proof", original, ready, finish).CombinedOutput(); err != nil {
		t.Fatalf("start isolated writer: %v %s", err, output)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := readNativeUnit(ctx, "systemctl", unit)
		_, readyErr := os.Stat(ready)
		if err == nil && state.SubState == "exited" && state.ExitStatus == "0" && readyErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("main process did not reach known exit", ctx.Err())
		case <-ticker.C:
		}
	}
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	var pending *Error
	if _, err := client.CaptureAccountTakeover(ctx, "takeover-systemd", binding, nil); !errors.As(err, &pending) || pending.Code != "MIGRATION_PENDING" {
		t.Fatalf("main exit substituted for descendant quiescence: %v", err)
	}
	if data, err := os.ReadFile(original); err != nil || string(data) != "before" {
		t.Fatal("takeover stopped or changed the old writer", err)
	}
	if err := os.WriteFile(finish, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	for confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+unit) != nil {
		select {
		case <-ctx.Done():
			t.Fatal("descendant did not exit naturally", ctx.Err())
		case <-ticker.C:
		}
	}
	receipt, err := client.CaptureAccountTakeover(ctx, "takeover-systemd", binding, nil)
	if err != nil {
		t.Fatal("quiescent capture failed", err)
	}
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bundle, retained, manifest, err := skillmanager.OpenAccountCapture(store, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if retained != receipt || len(manifest.Entries) != 1 || manifest.Entries[0].Size != int64(len("late-learning")) {
		t.Fatal("capture lost the legacy descendant's late write")
	}
	if data, err := os.ReadFile(original); err != nil || string(data) != "late-learning" {
		t.Fatal("original source was not preserved", err)
	}
}
