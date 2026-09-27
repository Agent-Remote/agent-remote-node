package runtimehelper

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestNativeSkillSystemdLifecycle(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated systemd PID 1 and private writable cgroup namespace")
	}
	for _, test := range []struct {
		name, ending              string
		unclean, stopWhileRunning bool
	}{
		{"normal", "exit 0", false, false}, {"error", "exit 7", true, false}, {"sigkill", "kill -KILL $$", true, false},
		{"graceful_stop", "stty -icanon -isig -echo\nprintf ready > /account/.claude/skills/ready\ntest \"$(dd bs=1 count=1 2>/dev/null | od -An -tu1 | tr -d ' ')\" = 3\nexit 0", false, true},
		{"canonical_interrupt", "trap 'exit 0' INT\nprintf ready > /account/.claude/skills/ready\nwhile :; do sleep 1; done", true, true},
		{"forced_stop", "stty -icanon -isig -echo\nprintf ready > /account/.claude/skills/ready\nwhile :; do sleep 1; done", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, spec, binding := nativeSkillFixture(t, false)
			spec = prepareNativeProofFilesystem(t, engine, spec)
			engine.config.RuntimeBinaryPath = "/proof/agent-remote-runtime"
			engine.config.ClaudeRuntimePath = filepath.Join(spec.RuntimeRoot, "bin", "claude")
			engine.config.SystemctlPath, engine.config.SystemdRunPath = "systemctl", "systemd-run"
			engine.config.CgroupRoot, engine.config.IPPath, engine.config.NFTPath = "/sys/fs/cgroup", "ip", "nft"
			spec.RuntimeConfig = sessionRuntimeConfigFromEngine(engine.config)
			spec.Policy = defaultRuntimePolicy
			if err := exec.Command("id", "-u", spec.Username).Run(); err != nil {
				for _, args := range [][]string{{"groupadd", "--gid", "12345", spec.Username}, {"useradd", "--uid", "12345", "--gid", "12345", "--no-create-home", "--shell", "/bin/sh", spec.Username}} {
					if output, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
						t.Fatalf("create test identity: %s, %v", output, err)
					}
				}
			}
			if err := os.Mkdir(filepath.Join(spec.SessionRoot, "tmux"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(filepath.Join(spec.SessionRoot, "tmux"), spec.RuntimeUID, spec.RuntimeGID); err != nil {
				t.Fatal(err)
			}
			if err := writeOwnedFile(processExitMarkerPath(spec), nil, 0o600, runtimeIdentity{UID: spec.RuntimeUID, GID: spec.RuntimeGID}); err != nil {
				t.Fatal(err)
			}
			program := "#!/bin/sh\nset -eu\nprintf learned > /account/.claude/skills/learned\ntest \"$(cat /home/runtime/.claude/skills/learned)\" = learned\n" + test.ending + "\n"
			if err := os.WriteFile(engine.config.ClaudeRuntimePath, []byte(program), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := engine.saveSpec(spec); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = exec.CommandContext(ctx, "systemctl", "stop", spec.UnitName).Run()
				_ = exec.CommandContext(ctx, "systemctl", "reset-failed", spec.UnitName).Run()
				_ = engine.cleanupTemp(ctx, spec)
				_ = engine.cleanupNativeSkillMount(spec)
				_ = exec.CommandContext(ctx, "ip", "netns", "delete", spec.NetworkNamespace).Run()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := engine.launch(ctx, spec); err != nil {
				log, _ := exec.Command("journalctl", "-u", spec.UnitName, "--no-pager", "-n", "20", "-o", "cat").CombinedOutput()
				t.Fatalf("real Native launch failed: %v\n%s", err, log)
			}
			digest := sha256.Sum256([]byte(spec.TmuxSessionName))
			if _, err := nativeTmuxCommand(ctx, engine.config, spec, "wait-for", "-S", fmt.Sprintf("agent-remote-client-%x", digest[:8])); err != nil {
				t.Fatal(err)
			}
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				if test.stopWhileRunning {
					bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
					if err != nil {
						t.Fatal(err)
					}
					_, readyErr := bundle.Stat("work/ready")
					_ = bundle.Close()
					if readyErr == nil {
						break
					}
				} else {
					active, err := engine.nativeSessionActive(spec)
					if err == nil && !active {
						break
					}
				}
				select {
				case <-ctx.Done():
					t.Fatal("tool did not become quiescent", ctx.Err())
				case <-ticker.C:
				}
			}
			result, err := engine.stopSession(ctx, map[string]any{"session_id": spec.SessionID})
			if err != nil || result["state_pending"] != true || result["skill_unclean"] != test.unclean {
				log, _ := exec.Command("journalctl", "-u", spec.UnitName, "--no-pager", "-n", "20", "-o", "cat").CombinedOutput()
				t.Fatalf("incorrect real Native finalization: %#v, %v\n%s", result, err, log)
			}
			bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if data, err := bundle.ReadFile("work/learned"); err != nil || string(data) != "learned" {
				t.Fatalf("tool data lost: %s, %v", data, err)
			}
			terminal := "published"
			if test.unclean {
				terminal = "detached"
			}
			frozen, err := skillmanager.ReadFinalization(bundle, binding)
			if err != nil {
				t.Fatal(err)
			}
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			acknowledged, err := client.AcknowledgeSkillFinalization(ctx, "systemd-ack", helperFinalizationAck(frozen, terminal))
			if err != nil {
				t.Fatal("real runtime acknowledgement failed", err)
			}
			if _, err := client.CleanupFinalizedSkillSession(ctx, "systemd-cleanup", acknowledged); err != nil {
				t.Fatal("real runtime cleanup failed", err)
			}
			if _, err := os.Stat(spec.SessionRoot); !os.IsNotExist(err) {
				t.Fatal("session view remains after terminal retention", err)
			}
			if _, err := bundle.Stat("work/learned"); err != nil {
				t.Fatal("transient deletion removed durable work", err)
			}
			if err := bundle.RemoveAll("work"); err != nil {
				t.Fatal(err)
			}
			record, manifest, err := client.ReadSkillFinalization(ctx, "systemd-finalization", binding)
			if err != nil || record.Unclean != test.unclean || record.State != terminal || record.ObjectsVersion != 1 {
				t.Fatal("real runtime lost frozen finalization after work removal", err)
			}
			found := false
			for _, entry := range manifest.Entries {
				if entry.Path != "learned" {
					continue
				}
				file, retained, err := client.OpenSkillFinalizationObject(ctx, "systemd-object", record, entry.SHA256)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(file)
				_ = file.Close()
				if err != nil || retained != entry || skillmanager.VerifyContent(entry, data) != nil || string(data) != "learned" {
					t.Fatal("actual runtime output changed in descriptor transfer", err)
				}
				found = true
			}
			if !found {
				t.Fatal("frozen finalization omitted the runtime output")
			}
		})
	}
}
