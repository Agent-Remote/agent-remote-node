package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func TestHelperFinalizationReclamationMountAliases(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_MOUNT_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated privileged Linux mount namespace")
	}
	for _, kind := range []string{"work_alias", "objects_alias", "bundle_alias", "object_alias", "objects_self_bind"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, capture := helperReclamationFixture(t, false, "started")
			bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			bundlePath := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
			source := filepath.Join(bundlePath, "work")
			target := filepath.Join(t.TempDir(), "alias")
			switch kind {
			case "objects_alias", "objects_self_bind":
				source = filepath.Join(bundlePath, "finalization", "objects")
			case "bundle_alias":
				source = bundlePath
			case "object_alias":
				matches, err := filepath.Glob(filepath.Join(bundlePath, "finalization", "objects", "*"))
				if err != nil || len(matches) != 1 {
					t.Fatal("unexpected object fixture", err)
				}
				source = matches[0]
			}
			if kind == "objects_self_bind" {
				target = source
			} else if kind == "object_alias" {
				err = os.WriteFile(target, nil, 0600)
			} else {
				err = os.Mkdir(target, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = unix.Unmount(target, 0) })
			server := NewServer("", -1, os.Geteuid(), engine)
			if err := server.reclaimFinalization(context.Background(), capture, helperReclamationGrant); err == nil {
				t.Fatal("mounted content allowed reclamation")
			}
			if _, err := bundle.Lstat("finalization/reclamation.json"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("mounted content gained intent", err)
			}
			if err := unix.Unmount(target, 0); err != nil {
				t.Fatal("reclamation removed another mount", err)
			}
			if err := server.reclaimFinalization(context.Background(), capture, helperReclamationGrant); err != nil {
				t.Fatal("unmounted input could not reclaim", err)
			}
			if _, complete, err := skillmanager.ReadFinalizationReclamation(bundle, capture.Binding); err != nil || !complete {
				t.Fatal("unmounted input lost completion", err)
			}
		})
	}
}
