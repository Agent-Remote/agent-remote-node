package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func requireNativePython(t *testing.T) map[string]string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root Linux Helper and system Python")
	}
	dependencies, err := discoverNativePython(context.Background(), 12345, 12345)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range dependencies {
		if target == "/usr/bin/python3" {
			return dependencies
		}
	}
	if os.Getenv("AGENT_REMOTE_TEST_PYTHON") == "1" {
		t.Fatal("required system Python dependency was not discovered")
	}
	t.Skip("system Python is not installed")
	return nil
}

func TestNativeSkillPythonDiscoveryAndCompatibility(t *testing.T) {
	dependencies := requireNativePython(t)
	if len(dependencies) < 2 {
		t.Fatal("generic and versioned interpreter paths must both be recognized")
	}
	if err := verifyNativePython(context.Background(), 12345, 12345, dependencies); err != nil {
		t.Fatal(err)
	}
	for _, target := range dependencies {
		for _, invalid := range []map[string]string{
			{"python3": target}, {"cpython-3.99-little-linux-unknown": target},
			{"python3": "/etc/shadow"},
		} {
			if err := verifyNativePython(context.Background(), 12345, 12345, invalid); err == nil {
				t.Fatal("unverified dependency accepted", invalid)
			}
		}
		break
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverNativePython(ctx, 12345, 12345); !errors.Is(err, context.Canceled) {
		t.Fatal("dependency discovery ignored cancellation", err)
	}
	if _, err := discoverNativePython(context.Background(), 0, 0); err == nil {
		t.Fatal("probe accepted root execution")
	}
}

func TestNativeSkillPythonRejectsCallerPathsAndUnboundedOutput(t *testing.T) {
	for _, target := range []string{"/tmp/python3", "/usr/bin/../bin/python3", "/usr/bin/sh", "/usr/bin/python3/extra", "/usr/local/bin/python3-custom"} {
		if _, file, err := openNativePython(target); err == nil {
			_ = file.Close()
			t.Fatal("unexpected executable path accepted", target)
		}
	}
	output := &pythonProbeOutput{}
	if _, err := output.Write([]byte(strings.Repeat("x", 128))); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, struct{ io.Reader }{strings.NewReader("x")}); err == nil || len(output.String()) != 128 {
		t.Fatal("probe output was not bounded")
	}
}

func TestNativeSkillPythonRejectsUnsafeInterpreterFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned interpreter fixtures")
	}
	elfBytes, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"safe_elf", "script", "fake_elf", "writable", "setuid", "owner", "external_link", "directory", "fifo", "link_loop"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "python3")
			if err := os.WriteFile(path, elfBytes, 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "script":
				err = os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)
			case "fake_elf":
				err = os.WriteFile(path, []byte{0x7f, 'E', 'L', 'F'}, 0o755)
			case "writable":
				err = os.Chmod(path, 0o777)
			case "setuid":
				err = os.Chmod(path, os.ModeSetuid|0o755)
			case "owner":
				err = os.Chown(path, 12345, 12345)
			case "external_link", "directory", "fifo", "link_loop":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "external_link":
					err = os.Symlink("../python3", path)
				case "directory":
					err = os.Mkdir(path, 0o755)
				case "fifo":
					err = unix.Mkfifo(path, 0o755)
				case "link_loop":
					err = os.Symlink("python3", path)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			parent, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			_, file, err := openNativePythonFile(parent, "python3")
			if err == nil {
				_ = file.Close()
			}
			if kind == "safe_elf" {
				if err != nil {
					t.Fatal("protected real ELF fixture was rejected", err)
				}
			} else if err == nil {
				t.Fatal("unsafe interpreter fixture was accepted")
			}
		})
	}
}

func TestNativeSkillPythonPreparationReplayPreservesOriginalPolicy(t *testing.T) {
	requireNativePython(t)
	engine, spec, binding := nativeSkillFixture(t, false)
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if len(receipt.Snapshot.Capture.RuntimeDependencies) == 0 {
		t.Fatal("real preparation did not seal discovered dependencies")
	}
	if err := bundle.WriteFile("work/learned", []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate a historical empty mapping. Installed Python must not silently upgrade it.
	receipt.Snapshot.Capture.RuntimeDependencies = nil
	writePythonSnapshotFixture(t, bundle, receipt.Snapshot)
	open := func(context.Context, string) (io.ReadCloser, error) {
		t.Fatal("preparation replay requested content")
		return nil, errors.New("unexpected read")
	}
	if err := engine.prepareNativeSkillSnapshot(context.Background(), spec, binding, skillmanager.Manifest{Version: 1}, open); err != nil {
		t.Fatal(err)
	}
	reopened, after, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(receipt, after) {
		t.Fatal("retry changed original runtime dependency policy")
	}
	if data, err := bundle.ReadFile("work/learned"); err != nil || string(data) != "preserve me" {
		t.Fatal("retry lost learned content", err)
	}
}

func TestNativeSkillPythonMissingDependencyBlocksMountButNotCapture(t *testing.T) {
	engine, spec, binding := nativeSkillFixture(t, false)
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	receipt.Snapshot.Capture.RuntimeDependencies = map[string]string{"cpython-3.99-unavailable": "/usr/bin/python3.99"}
	writePythonSnapshotFixture(t, bundle, receipt.Snapshot)
	if err := bundle.Symlink("/usr/bin/python3.99", "work/python"); err != nil {
		t.Fatal(err)
	}
	if err := engine.mountNativeSkills(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "runtime_dependency_missing") {
		t.Fatal("mount did not reject missing Python", err)
	}
	if _, err := os.Lstat(filepath.Join(spec.SessionRoot, "skill-work")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dependency failure modified mount state", err)
	}
	if _, err := skillmanager.FinalizeWorkTree(context.Background(), bundle, binding, true); err != nil {
		t.Fatal("missing interpreter prevented historical content recovery", err)
	}
}

func writePythonSnapshotFixture(t *testing.T, bundle *os.Root, snapshot skillmanager.PreparedSnapshot) {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	file, err := bundle.OpenFile("snapshot.json", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write(data)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
}
