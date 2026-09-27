package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func serveFirstUseHelper(t *testing.T, config runtimehelper.EngineConfig) (string, func()) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "helper.sock")
	server := runtimehelper.NewServer(socket, -1, os.Geteuid(), runtimehelper.NewEngine(config))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error("Helper shutdown failed", err)
			}
		})
	}
	t.Cleanup(stop)
	waitFirstUse(t, func() bool { _, err := os.Stat(socket); return err == nil })
	return socket, stop
}

func waitFirstUse(t *testing.T, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("first-use proof condition did not become ready")
		case <-ticker.C:
		}
	}
}

func firstUseLegacyWriter(t *testing.T, accountID, source string) func() {
	t.Helper()
	control := t.TempDir()
	ready, finish := filepath.Join(control, "ready"), filepath.Join(control, "finish")
	unit := "agent-remote-session-" + strings.ReplaceAll(accountID, "-", "")[:12] + ".service"
	stateFile := filepath.Join(source, "manual", "memory.txt")
	program := `(printf ready > "$2"; while [ ! -e "$3" ]; do sleep 0.05; done; printf late-learning > "$1") & exit 0`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "systemd-run", "--unit="+unit,
		"--property=KillMode=process", "--property=RemainAfterExit=yes",
		"/bin/sh", "-c", program, "first-use-proof", stateFile, ready, finish).CombinedOutput(); err != nil {
		t.Fatalf("start isolated legacy writer: %v %s", err, output)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(finish, []byte("release"), 0600)
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "systemctl", "stop", unit).Run()
	})
	waitFirstUse(t, func() bool {
		_, err := os.Stat(ready)
		output, showErr := exec.CommandContext(ctx, "systemctl", "show", "--property=SubState", "--value", unit).Output()
		return err == nil && showErr == nil && strings.TrimSpace(string(output)) == "exited"
	})
	return func() {
		if err := os.WriteFile(finish, []byte("release"), 0600); err != nil {
			t.Fatal(err)
		}
		waitFirstUse(t, func() bool {
			data, err := os.ReadFile(stateFile)
			events, groupErr := os.ReadFile(filepath.Join("/sys/fs/cgroup/system.slice", unit, "cgroup.events"))
			return err == nil && string(data) == "late-learning" &&
				(os.IsNotExist(groupErr) || groupErr == nil && strings.Contains(string(events), "populated 0"))
		})
	}
}
