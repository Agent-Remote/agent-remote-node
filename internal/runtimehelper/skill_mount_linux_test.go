package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func requireSkillMountProof(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_MOUNT_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated Linux mount namespace with CAP_SYS_ADMIN and bubblewrap")
	}
}

func TestNativeSkillMountAliasesAndSystemReadOnly(t *testing.T) {
	requireSkillMountProof(t)
	engine, spec, binding := nativeSkillFixture(t, false)
	spec = prepareNativeProofFilesystem(t, engine, spec)
	program := `#!/bin/sh
set -eu
test "$(id -u)" = 12345
for root in /home/runtime/.claude/skills /account/.claude/skills; do
  test ! -e "$root/shared-only"
  test ! -e "$root/agent-remote-device"
  for name in ego-browser; do
    test -r "$root/$name/SKILL.md"
    if (printf corrupt >> "$root/$name/SKILL.md") 2>/dev/null; then exit 12; fi
    if rm "$root/$name/SKILL.md" 2>/dev/null; then exit 13; fi
  done
done
printf 'from home' > /home/runtime/.claude/skills/learned
test "$(cat /account/.claude/skills/learned)" = 'from home'
printf 'from account' > /account/.claude/skills/learned
test "$(cat /home/runtime/.claude/skills/learned)" = 'from account'
printf '#!/bin/sh\nprintf executable' > /account/.claude/skills/script
chmod 0700 /account/.claude/skills/script
test "$(/home/runtime/.claude/skills/script)" = executable
`
	if err := os.WriteFile(filepath.Join(spec.RuntimeRoot, "bin", "claude"), []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}
	spec.Timezone, spec.Locale = "UTC", "C"
	if err := engine.mountNativeSkills(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.cleanupNativeSkillMount(spec); err != nil {
			t.Error(err)
		}
	})
	if err := engine.mountNativeSkills(context.Background(), spec); err != nil {
		t.Fatal("identical mount retry failed", err)
	}
	command := exec.Command("bwrap", bubblewrapArgs(engine.config, spec)...)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(spec.RuntimeUID), Gid: uint32(spec.RuntimeGID)}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("non-root Bubblewrap alias/write proof failed: %s, %v", output, err)
	}
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if data, err := bundle.ReadFile("work/learned"); err != nil || string(data) != "from account" {
		t.Fatalf("runtime writes missed durable copy: %s, %v", data, err)
	}
	if _, err := skillmanager.FinalizeWorkTree(context.Background(), bundle, binding, false); err != nil {
		t.Fatal(err)
	}
	if err := engine.mountNativeSkills(context.Background(), spec); err == nil {
		t.Fatal("finalized work was allowed to relaunch")
	}
	if err := engine.cleanupNativeSkillMount(spec); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(spec.SessionRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Stat("work/learned"); err != nil {
		t.Fatal("transient cleanup deleted persistent mounted content", err)
	}
	// Cleanup is idempotent after the session root has been removed.
}

func TestNativeSkillMountSelectedDeviceRemainsReadOnly(t *testing.T) {
	requireSkillMountProof(t)
	engine, spec, binding := nativeSkillFixture(t, false)
	proxy := filepath.Join(filepath.Dir(engine.config.StateRoot), "device-proxy")
	if err := os.WriteFile(proxy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine.config.DeviceProxyPath = proxy
	spec.DeviceControlProtocolVersion, spec.DeviceProxyPath = 1, proxy
	spec.DeviceControlDirectory = filepath.Join(spec.SessionRoot, "device-control")
	spec.RuntimeConfig = sessionRuntimeConfigFromEngine(engine.config)
	var err error
	spec.Argv, err = managedDeviceControlArgv(spec.SessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(spec.DeviceControlDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	binding.SystemReleases.Device = skillmanager.DeviceSkillRelease{NodeReleaseVersion: config.DefaultVersion, ProtocolVersion: 1}
	if err := os.RemoveAll(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)); err != nil {
		t.Fatal(err)
	}
	if err := engine.prepareNativeSkillSnapshot(context.Background(), spec, binding, skillmanager.Manifest{Version: 1}, func(context.Context, string) (io.ReadCloser, error) {
		return nil, errors.New("empty snapshot must not request objects")
	}); err != nil {
		t.Fatal(err)
	}
	spec = prepareNativeProofFilesystem(t, engine, spec)
	program := `#!/bin/sh
set -eu
for root in /home/runtime/.claude/skills /account/.claude/skills; do
  test -r "$root/agent-remote-device/SKILL.md"
  if (printf corrupt >> "$root/agent-remote-device/SKILL.md") 2>/dev/null; then exit 12; fi
  if rm "$root/agent-remote-device/SKILL.md" 2>/dev/null; then exit 13; fi
done
`
	if err := os.WriteFile(filepath.Join(spec.RuntimeRoot, "bin", "claude"), []byte(program), 0o755); err != nil {
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
		t.Fatalf("selected device skill mount failed: %s, %v", output, err)
	}
}

func TestNativeSkillHistoricalPinsBlockMountButPreserveFinalization(t *testing.T) {
	for _, kind := range []string{"absent", "different_release"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, binding := nativeSkillFixture(t, false)
			bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if kind == "absent" {
				binding.SystemReleases = skillmanager.SystemReleasePins{}
			} else {
				binding.SystemReleases.EgoBrowser.Version = "older-release"
			}
			receipt.Snapshot.Binding = binding
			data, err := json.Marshal(receipt.Snapshot)
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
			if err := engine.mountNativeSkills(context.Background(), spec); err == nil {
				t.Fatal("historical snapshot mounted current system artifacts")
			}
			if _, err := os.Stat(filepath.Join(spec.SessionRoot, "skill-work")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("pin failure changed runtime mount state", err)
			}
			if _, err := skillmanager.FinalizeWorkTree(context.Background(), bundle, binding, true); err != nil {
				t.Fatal("historical artifact pin blocked data preservation", err)
			}
		})
	}
}

func TestNativeSkillMountRechecksInstalledSystemTreeOnReplay(t *testing.T) {
	requireSkillMountProof(t)
	engine, spec, _ := nativeSkillFixture(t, false)
	if err := engine.mountNativeSkills(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.cleanupNativeSkillMount(spec); err != nil {
			t.Error(err)
		}
	})
	directory := filepath.Join(spec.SessionRoot, "system-skills", ".claude", "skills", "ego-browser")
	if err := os.WriteFile(filepath.Join(directory, "unexpected.md"), []byte("extra instructions"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := engine.mountNativeSkills(context.Background(), spec); err == nil {
		t.Fatal("mount replay accepted extra system files")
	}
	if _, err := os.Stat(filepath.Join(directory, "unexpected.md")); err != nil {
		t.Fatal("verification repaired a mounted tree", err)
	}
}

func prepareNativeProofFilesystem(t *testing.T, engine Engine, spec SessionSpec) SessionSpec {
	t.Helper()
	root := filepath.Dir(engine.config.StateRoot)
	for _, path := range []string{root, filepath.Dir(root), engine.config.StateRoot, filepath.Join(engine.config.StateRoot, "sessions"), spec.SessionRoot} {
		if err := os.Chmod(path, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	spec.RuntimeRoot = filepath.Join(root, "artifact")
	for _, path := range []string{spec.WorkspacePath, filepath.Join(spec.RuntimeRoot, "bin"), filepath.Join(spec.AccountPath, ".claude", "skills"), filepath.Join(spec.SessionRoot, "tmp")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(filepath.Join(spec.AccountPath, ".claude"), spec.RuntimeUID, spec.RuntimeGID); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(spec.AccountPath, ".claude.json"):                     "{}",
		filepath.Join(spec.AccountPath, ".claude", "skills", "shared-only"): "must stay hidden",
		filepath.Join(spec.SessionRoot, "passwd"):                           "runtime:x:12345:12345::/home/runtime:/bin/sh\n",
		filepath.Join(spec.SessionRoot, "group"):                            "runtime:x:12345:\n",
		filepath.Join(spec.SessionRoot, "timezone"):                         "UTC\n",
		filepath.Join(spec.SessionRoot, "resolv.conf"):                      "",
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec.Timezone, spec.Locale = "UTC", "C"
	return spec
}

func TestNativeSkillBusyMountPreservesSessionDirectory(t *testing.T) {
	requireSkillMountProof(t)
	engine, spec, _ := nativeSkillFixture(t, false)
	if err := engine.mountNativeSkills(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sleep", "30")
	command.Dir = filepath.Join(spec.SessionRoot, "skill-work")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if err := engine.cleanupNativeSkillMount(spec); err != nil {
			t.Error(err)
		}
	}()
	if err := engine.cleanupNativeSkillMount(spec); err == nil || !strings.Contains(err.Error(), "unmount") {
		t.Fatalf("busy skill mount should block cleanup: %v", err)
	}
	if _, err := os.Stat(engine.specPath(spec.SessionID)); err != nil {
		t.Fatal("busy mount lost session files", err)
	}
}
