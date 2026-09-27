package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestManagedLaunchSystemdRebootMountGuards(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated privileged Linux mount namespace")
	}
	for _, kind := range []string{"work_alias", "work_child_alias", "bundle_alias", "runtime_tmp", "runtime_root"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec := previousBootSkillFixture(t, "started")
			bundle := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
			source, target := filepath.Join(bundle, "work"), filepath.Join(t.TempDir(), "alias")
			switch kind {
			case "work_child_alias":
				source = filepath.Join(source, "child")
				if err := os.Mkdir(source, 0o700); err != nil {
					t.Fatal(err)
				}
			case "bundle_alias":
				source = bundle
			case "runtime_tmp":
				source, target = t.TempDir(), filepath.Join(spec.SessionRoot, "tmp")
			case "runtime_root":
				source, target = spec.SessionRoot, spec.SessionRoot
			}
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = unix.Unmount(target, 0) })
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			if _, err := client.ReconcileSkillSession(context.Background(), "mounted", input.Snapshot.NodeID, spec.SessionID); err == nil {
				t.Fatal("reappearing kernel mount permitted previous-boot capture")
			}
			if err := unix.Unmount(target, 0); err != nil {
				t.Fatal("recovery removed the current mount", err)
			}
			observation, err := client.ReconcileSkillSession(context.Background(), "unmounted", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || observation.Record == nil || !observation.Record.Unclean {
				t.Fatal("verified mount absence did not allow recovery", err)
			}
			ack, err := client.AcknowledgeSkillFinalization(context.Background(), "retained", helperFinalizationAck(*observation.Record, "detached"))
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := client.CleanupFinalizedSkillSession(context.Background(), "new-mount", ack); err == nil {
				t.Fatal("publication acknowledgement authorized deleting a current mount")
			}
			if err := unix.Unmount(target, 0); err != nil {
				t.Fatal("cleanup removed the current mount", err)
			}
			if _, err := client.CleanupFinalizedSkillSession(context.Background(), "safe-cleanup", ack); err != nil {
				t.Fatal(err)
			}
		})
	}
}
