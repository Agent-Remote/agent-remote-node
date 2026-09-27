package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperCapturePendingRequiresFreshStoppedEvidence(t *testing.T) {
	for _, fault := range []string{"", "running", "populated", "missing_termination", "corrupt_termination", "existing_finalization", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			engine, spec, _ := stoppedExportFixture(t)
			root := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "running":
				engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, "running", strings.Repeat("a", 32))
			case "populated":
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
				mustExportFixture(t, os.MkdirAll(group, 0700))
				mustExportFixture(t, os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0600))
			case "missing_termination":
				mustExportFixture(t, os.Remove(filepath.Join(root, "termination.json")))
			case "corrupt_termination":
				mustExportFixture(t, os.WriteFile(filepath.Join(root, "termination.json"), []byte("{}"), 0600))
			case "existing_finalization":
				mustExportFixture(t, os.Mkdir(filepath.Join(root, "finalization"), 0700))
			case "cancelled":
				cancel()
			}
			store, err := engine.openExistingSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			bundle, session, err := skillmanager.OpenSessionSnapshot(store, spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			observed, err := engine.capturePendingObservation(ctx, store, bundle, session, syscall.ENOSPC)
			if fault != "" {
				if err == nil || observed.Pending != nil {
					t.Fatal("uncertain writers confirmed", observed, err)
				}
				return
			}
			if err != nil || observed.Validate(session.Snapshot.Binding.NodeID, spec.SessionID) != nil || observed.Pending.Code != "insufficient_storage" || !observed.Pending.Unclean {
				t.Fatal(observed, err)
			}
			for _, code := range []string{"quota_exceeded", "portability_error", "capture_failed"} {
				observed, err = engine.capturePendingObservation(ctx, store, bundle, session, errors.New(code+": private diagnostic"))
				if err != nil || observed.Pending.Code != code {
					t.Fatal(code, observed, err)
				}
			}
		})
	}
}

func TestHelperCapturePendingQuotaReconcilesAfterCorrection(t *testing.T) {
	engine, input, spec, launch := preparedManagedLaunch(t)
	sealReconciliationLaunch(t, engine, launch)
	engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, "clean", strings.Repeat("a", 32))
	root := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
	bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Close()
	snapshot := session.Snapshot
	snapshot.Capture.DirectoryBytes = 8
	writeRebootFixtureJSON(t, filepath.Join(root, "snapshot.json"), snapshot)
	mustExportFixture(t, os.WriteFile(filepath.Join(root, "work", "learned"), []byte("extra runtime learning"), 0600))
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	for i := 0; i < 2; i++ {
		observation, err := client.ReconcileSkillSession(context.Background(), "quota-reconcile", input.Snapshot.NodeID, spec.SessionID)
		if err != nil || observation.Pending == nil || observation.Pending.Code != "quota_exceeded" || observation.Pending.Unclean {
			t.Fatal("stopped quota capture not reported", observation, err)
		}
	}
	mustExportFixture(t, os.Remove(filepath.Join(root, "work", "learned")))
	observation, err := client.ReconcileSkillSession(context.Background(), "quota-recovered", input.Snapshot.NodeID, spec.SessionID)
	if err != nil || observation.Record == nil || observation.Record.Unclean || observation.Pending != nil {
		t.Fatal("original clean capture did not recover", observation, err)
	}
}
