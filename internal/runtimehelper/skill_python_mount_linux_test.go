package runtimehelper

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestNativeSkillPythonVenvSurvivesCaptureAndNextMountedSession(t *testing.T) {
	requireSkillMountProof(t)
	requireNativePython(t)
	engine, original, binding := nativeSkillFixture(t, false)
	spec := prepareNativeProofFilesystem(t, engine, original)
	runPythonSkillProof(t, engine, spec, `
mkdir -p /home/runtime/.claude/skills/learning
/usr/bin/python3 -m venv --without-pip /home/runtime/.claude/skills/learning/.venv
printf 'value = 42\n' > /home/runtime/.claude/skills/learning/learned.py
cd /home/runtime/.claude/skills/learning
.venv/bin/python -c 'import learned; assert learned.value == 42'
`)
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	// The foreground Bubblewrap process and PID namespace have exited. This proves the actual
	// mounted Python round trip, not Server publication, systemd lifecycle or real Claude use.
	if _, err := engine.finalizeNativeSkillSession(context.Background(), spec, nativeTermination{}); err != nil {
		t.Fatal(err)
	}
	file, record, err := skillmanager.OpenFinalizationManifest(bundle, binding)
	if err != nil {
		t.Fatal(err)
	}
	var manifest skillmanager.Manifest
	err = json.NewDecoder(file).Decode(&manifest)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	links := 0
	for _, entry := range manifest.Entries {
		if entry.Kind == "runtime_link" {
			links++
			if entry.Target != "/usr/bin/python3" || !strings.HasPrefix(entry.Dependency, "cpython-3.") || entry.Size != 0 || entry.SHA256 != "" {
				t.Fatal("interpreter link did not remain a verified external dependency", entry)
			}
		}
	}
	if links != 1 {
		t.Fatalf("expected one real venv external interpreter link, got %d", links)
	}
	if err := engine.cleanupNativeSkillMount(spec); err != nil {
		t.Fatal(err)
	}
	next := original
	next.SessionID = "66666666-6666-4666-8666-666666666666"
	next.SkillSnapshotID = "77777777-7777-4777-8777-777777777777"
	next.SessionRoot = filepath.Join(engine.config.StateRoot, "sessions", next.SessionID)
	next.UnitName = "agent-remote-session-" + shortDigest(next.SessionID, 12) + ".service"
	next.NetworkNamespace = "ar-" + shortDigest(next.SessionID, 10)
	next.TmuxSocketPath = filepath.Join(next.SessionRoot, "tmux", "tmux.sock")
	if err := os.Mkdir(next.SessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	nextBinding := binding
	nextBinding.SessionID, nextBinding.SnapshotID, nextBinding.InitialTreeDigest = next.SessionID, next.SkillSnapshotID, record.TreeDigest
	open := func(_ context.Context, digest string) (io.ReadCloser, error) {
		object, _, err := skillmanager.OpenFinalizationObject(bundle, record, digest)
		return object, err
	}
	if err := engine.prepareNativeSkillSnapshot(context.Background(), next, nextBinding, manifest, open); err != nil {
		t.Fatal("next preparation could not restore real venv", err)
	}
	next = prepareNativeProofFilesystem(t, engine, next)
	runPythonSkillProof(t, engine, next, `
cd /home/runtime/.claude/skills/learning
.venv/bin/python -c 'import sys, learned; assert learned.value == 42; assert sys.prefix.endswith("learning/.venv"); assert sys.prefix != sys.base_prefix'
test -L .venv/bin/python3
test "$(readlink .venv/bin/python3)" = /usr/bin/python3
printf 'next session inherited Python state' > result
`)
	nextBundle, _, err := engine.retainedNativeSkillSession(next.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer nextBundle.Close()
	if data, err := nextBundle.ReadFile("work/learning/result"); err != nil || string(data) != "next session inherited Python state" {
		t.Fatal("restored interpreter failed to write next session state", err)
	}
}

func runPythonSkillProof(t *testing.T, engine Engine, spec SessionSpec, program string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(spec.RuntimeRoot, "bin", "claude"), []byte("#!/bin/sh\nset -eu\ntest \"$(id -u)\" = 12345\n"+program), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := engine.mountNativeSkills(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.cleanupNativeSkillMount(spec); err != nil {
			t.Error(err)
		}
	})
	command := exec.Command("bwrap", bubblewrapArgs(engine.config, spec)...)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(spec.RuntimeUID), Gid: uint32(spec.RuntimeGID)}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("non-root mounted Python proof failed: %s, %v", output, err)
	}
}
