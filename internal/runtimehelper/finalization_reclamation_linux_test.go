package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func helperReclamationFixture(t *testing.T, previousBoot bool, phase string) (Engine, SessionSpec, skillmanager.FinalizationRecord) {
	t.Helper()
	var engine Engine
	var spec SessionSpec
	if previousBoot {
		engine, _, spec = previousBootSkillFixture(t, phase)
	} else {
		var launch skillmanager.SessionLaunch
		engine, _, spec, launch = preparedManagedLaunch(t)
		store, err := engine.openExistingSkillStateRoot()
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if phase != "prepared" {
			if err := skillmanager.BeginSessionLaunch(store, launch); err != nil {
				t.Fatal(err)
			}
			if phase == "started" {
				if err := skillmanager.FinishSessionLaunch(store, launch, strings.Repeat("a", 32)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	engine.config.IPPath = writeTestCommand(t, "reclamation-ip", "test \"$1 $2\" = 'netns list'")
	bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	capture, err := skillmanager.FinalizeWorkTreeWithPolicy(context.Background(), bundle, session.Snapshot.Binding, false, engine.finalizationCopyPolicy())
	if err != nil {
		t.Fatal(err)
	}
	capture, err = skillmanager.AcknowledgeFinalization(context.Background(), bundle, helperFinalizationAck(capture, "published"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := Map(finalizationCleanupRequest{Capture: capture})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.cleanupFinalization(context.Background(), Request{Version: ProtocolVersion, RequestID: "cleaned", Operation: finalizationCleanupOperation, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	return engine, spec, capture
}

func helperReclamationGrant(_ context.Context, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error) {
	binding := ack.Capture.Binding
	now := time.Now()
	return skillmanager.ReclamationAuthorization{
		Version: 1, RequestID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", NodeID: binding.NodeID, UserID: binding.UserID,
		AccountID: binding.AccountID, SessionID: binding.SessionID, SnapshotID: binding.SnapshotID,
		FinalizationID: ack.Receipt.ID, CheckpointID: *ack.Receipt.CheckpointID, TreeDigest: ack.Capture.TreeDigest,
		Unclean: ack.Capture.Unclean, PublicationID: ack.Publication.ID, PublicationAttempt: ack.Publication.Attempt,
		PublicationStatus: ack.Publication.Status, VerifiedAt: now.UTC(), ExpiresAt: now.Add(time.Minute).UTC(),
	}, now.Add(time.Minute), nil
}

func TestHelperFinalizationReclamationDeletesOnlyOriginalContent(t *testing.T) {
	for _, previous := range []bool{false, true} {
		for _, phase := range []string{"prepared", "starting", "started"} {
			if !previous && phase == "prepared" {
				continue
			}
			t.Run(map[bool]string{false: "same_boot_", true: "previous_boot_"}[previous]+phase, func(t *testing.T) {
				engine, spec, capture := helperReclamationFixture(t, previous, phase)
				server := NewServer("", -1, os.Geteuid(), engine)
				if err := server.reclaimFinalization(context.Background(), capture, helperReclamationGrant); err != nil {
					t.Fatal(err)
				}
				// A new Helper instance replays completion without renewing remote authority.
				restarted := NewServer("", -1, os.Geteuid(), engine)
				if err := restarted.reclaimFinalization(context.Background(), capture, nil); err != nil {
					t.Fatal("completion replay failed", err)
				}
				bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				defer bundle.Close()
				if intent, complete, err := skillmanager.ReadFinalizationReclamation(bundle, capture.Binding); err != nil || !complete || intent == nil {
					t.Fatal("deletion lost audit", err)
				}
				for _, path := range []string{"work", "finalization/objects"} {
					if _, err := bundle.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("retained content survived", path, err)
					}
				}
				for _, path := range []string{"snapshot.json", "runtime.json", "baseline.json", "termination.json", "finalization/manifest.json", "finalization/record.json", "finalization/acknowledgement.json", "finalization/runtime-cleanup.json"} {
					if _, err := bundle.Lstat(path); err != nil {
						t.Fatal("audit metadata removed", path, err)
					}
				}
			})
		}
	}
}

func TestHelperFinalizationReclamationPreservesUncertainResources(t *testing.T) {
	for _, kind := range []string{"runtime", "runtime_link", "running", "replacement", "exited", "unknown_unit", "cgroup", "network", "unknown_network", "missing_launch", "missing_draft", "corrupt_launch", "missing_cleanup", "reader", "missing_authority", "expired_authority", "changed_work", "after_authority", "previous_unit", "previous_cgroup"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, capture := helperReclamationFixture(t, strings.HasPrefix(kind, "previous_"), "started")
			bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			authorize := helperReclamationGrant
			switch kind {
			case "runtime":
				err = os.Mkdir(spec.SessionRoot, 0700)
			case "runtime_link":
				err = os.Symlink("missing", spec.SessionRoot)
			case "running", "replacement", "exited", "previous_unit":
				state, invocation := "failed", strings.Repeat("a", 32)
				if kind == "running" || kind == "exited" {
					state = kind
				}
				if kind == "replacement" {
					invocation = strings.Repeat("b", 32)
				}
				engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, state, invocation)
			case "unknown_unit":
				engine.config.SystemctlPath = writeTestCommand(t, "uncertain-systemctl", "exit 1")
			case "cgroup", "previous_cgroup":
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
				err = os.MkdirAll(group, 0700)
				if err == nil {
					err = os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0600)
				}
			case "network":
				engine.config.IPPath = writeTestCommand(t, "present-network", "echo "+spec.NetworkNamespace)
			case "unknown_network":
				engine.config.IPPath = writeTestCommand(t, "unknown-network", "exit 1")
			case "missing_launch":
				err = os.Remove(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"))
			case "missing_draft":
				err = os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json"))
			case "corrupt_launch":
				err = os.WriteFile(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"), []byte("{}"), 0600)
			case "missing_cleanup":
				err = bundle.Remove("finalization/runtime-cleanup.json")
			case "reader":
				var hold *os.File
				hold, _, err = skillmanager.HoldFinalizationContent(bundle, capture.Binding)
				if err == nil {
					defer hold.Close()
				}
			case "missing_authority":
				authorize = nil
			case "expired_authority":
				authorize = func(ctx context.Context, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error) {
					grant, _, err := helperReclamationGrant(ctx, ack)
					return grant, time.Now().Add(-time.Second), err
				}
			case "changed_work":
				err = bundle.WriteFile("work/new", []byte("uncaptured"), 0600)
			case "after_authority":
				authorize = func(ctx context.Context, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error) {
					if err := os.Mkdir(spec.SessionRoot, 0700); err != nil {
						t.Fatal(err)
					}
					return helperReclamationGrant(ctx, ack)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			if err := server.reclaimFinalization(context.Background(), capture, authorize); err == nil {
				t.Fatal("uncertain resource permitted deletion")
			}
			if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "original" {
				t.Fatal("rejected reclamation lost work", err)
			}
			if intent, _, err := skillmanager.ReadFinalizationReclamation(bundle, capture.Binding); err != nil || intent != nil {
				t.Fatal("rejected reclamation marked content", err)
			}
		})
	}
}

func TestHelperFinalizationReclamationResumesPartialIntentWithoutAuthorization(t *testing.T) {
	engine, spec, capture := helperReclamationFixture(t, false, "started")
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	ack, err := skillmanager.ReadFinalizationAcknowledgement(bundle, capture.Binding)
	if err != nil {
		t.Fatal(err)
	}
	grant, deadline, _ := helperReclamationGrant(context.Background(), ack)
	intent, err := skillmanager.MarkFinalizationReclamation(context.Background(), bundle, capture, grant, deadline, spec.SessionRoot)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verify := func(ctx context.Context) error {
		if _, err := bundle.Lstat("work/notes"); errors.Is(err, os.ErrNotExist) {
			cancel()
		}
		return ctx.Err()
	}
	if err := skillmanager.ReclaimFinalizationContent(ctx, bundle, intent, verify); !errors.Is(err, context.Canceled) {
		t.Fatal("fixture did not interrupt actual deletion", err)
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	if err := server.reclaimFinalization(context.Background(), capture, func(context.Context, skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error) {
		t.Error("resume requested fresh authority")
		return skillmanager.ReclamationAuthorization{}, time.Time{}, errors.New("unexpected authorization")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHelperFinalizationReclamationLifecycleLockIsCancellable(t *testing.T) {
	engine, _, capture := helperReclamationFixture(t, false, "started")
	server := NewServer("", -1, os.Geteuid(), engine)
	server.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- server.reclaimFinalization(ctx, capture, helperReclamationGrant) }()
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiting reclamation ignored cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reclamation blocked on lifecycle lock after cancellation")
	}
	server.mu.Unlock()
}
