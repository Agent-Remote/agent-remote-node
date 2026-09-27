package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Model coherent disk records from an earlier kernel without changing the host's boot identity.
func previousBootSkillFixture(t *testing.T, phase string) (Engine, ManagedSessionSpecRequest, SessionSpec) {
	t.Helper()
	engine, input, spec, launch := preparedManagedLaunchWithConfig(t, func(engine *Engine, _ *ManagedSessionSpecRequest) {
		if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") == "1" {
			engine.config.SystemctlPath, engine.config.IPPath = "systemctl", "ip"
			engine.config.CgroupRoot = "/sys/fs/cgroup"
		}
	})
	spec.BootID = "99999999-9999-4999-8999-999999999999"
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	launch.Spec.SpecDigest = hex.EncodeToString(digest[:])
	launch.BootID = spec.BootID
	draft := struct {
		Version     int             `json:"version"`
		InputDigest string          `json:"input_digest"`
		SpecDigest  string          `json:"spec_digest"`
		Spec        json.RawMessage `json:"spec"`
	}{1, launch.Spec.InputDigest, launch.Spec.SpecDigest, data}
	writeRebootFixtureJSON(t, filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json"), draft)
	writeRebootFixtureJSON(t, filepath.Join(engine.config.SkillStateRoot, "spec-intent-"+spec.SessionID+".json"), launch.Spec)
	writeRebootFixtureJSON(t, filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, "runtime.json"), skillmanager.RuntimeBinding{
		Backend: "native", ResourceID: spec.UnitName, BootID: spec.BootID, UID: spec.RuntimeUID, GID: spec.RuntimeGID,
	})
	if err := os.WriteFile(engine.specPath(spec.SessionID), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if phase != "prepared" {
		store, err := engine.openExistingSkillStateRoot()
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if err := skillmanager.BeginSessionLaunch(store, launch); err != nil {
			t.Fatal(err)
		}
		if phase == "observed" {
			if err := skillmanager.ObserveSessionLaunch(store, launch, strings.Repeat("a", 32)); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "started" {
			if err := skillmanager.FinishSessionLaunch(store, launch, strings.Repeat("a", 32)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" {
		engine.config.IPPath = writeTestCommand(t, "reboot-ip", "test \"$1 $2\" = 'netns list'")
	}
	return engine, input, spec
}

func writeRebootFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHelperFinalizationRebootRecoversAllPreparedLaunchPhases(t *testing.T) {
	for _, phase := range []string{"prepared", "starting", "observed", "started"} {
		t.Run(phase, func(t *testing.T) {
			engine, input, spec := previousBootSkillFixture(t, phase)
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
				t.Fatal(err)
			}
			observation, err := client.ReconcileSkillSession(context.Background(), "reboot", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || observation.Record == nil || !observation.Record.Unclean || observation.State != "finalized" {
				t.Fatal("previous-boot data was not frozen as unclean", observation, err)
			}
			replay, err := client.DrainUnadmittedSkillSession(context.Background(), "replay", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || replay.Record == nil || *replay.Record != *observation.Record {
				t.Fatal("reboot changed frozen replay", err)
			}
			if _, err := client.CleanupFinalizedSkillSession(context.Background(), "premature", *observation.Record); err == nil {
				t.Fatal("local reboot capture authorized cleanup")
			}
			ack, err := client.AcknowledgeSkillFinalization(context.Background(), "saved", helperFinalizationAck(*observation.Record, "detached"))
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := client.CleanupFinalizedSkillSession(context.Background(), "cleanup", ack); err != nil {
					t.Fatal("previous-boot cleanup failed", err)
				}
			}
			if _, err := os.Lstat(spec.SessionRoot); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("old transient root remains", err)
			}
			bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "original" {
				t.Fatal("reboot cleanup removed retained work", err)
			}
		})
	}
}

func TestHelperFinalizationRebootRefusesReappearingResources(t *testing.T) {
	for _, kind := range []string{"running", "inactive_unit", "cgroup", "network", "network_unknown", "spec", "linked_root", "linked_work", "corrupt_launch", "missing_draft"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec := previousBootSkillFixture(t, "started")
			var actions string
			switch kind {
			case "running", "inactive_unit":
				engine.config.SystemctlPath, actions = reconciliationSystemctl(t, spec, "running", strings.Repeat("a", 32))
				if kind == "inactive_unit" {
					data, _ := os.ReadFile(engine.config.SystemctlPath)
					data = []byte(strings.ReplaceAll(string(data), "ActiveState=active", "ActiveState=inactive"))
					if err := os.WriteFile(engine.config.SystemctlPath, data, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "cgroup":
				if err := os.MkdirAll(filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName), 0o700); err != nil {
					t.Fatal(err)
				}
			case "network":
				engine.config.IPPath = writeTestCommand(t, "reappeared-ip", "printf '%s\\n' "+spec.NetworkNamespace)
			case "network_unknown":
				engine.config.IPPath = writeTestCommand(t, "invalid-ip", "echo 'not an inventory'")
			case "spec":
				if err := os.WriteFile(engine.specPath(spec.SessionID), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "linked_root", "linked_work":
				path := spec.SessionRoot
				if kind == "linked_work" {
					path = filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, "work")
				}
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-original", path); err != nil {
					t.Fatal(err)
				}
			case "corrupt_launch":
				writeRebootFixtureJSON(t, filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"), map[string]any{"version": 1})
			case "missing_draft":
				if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json")); err != nil {
					t.Fatal(err)
				}
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			if _, err := client.ReconcileSkillSession(context.Background(), "reboot", input.Snapshot.NodeID, spec.SessionID); err == nil {
				t.Fatal("uncertain previous-boot resources allowed capture")
			}
			if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID}); err == nil {
				t.Fatal("manual stop bypassed previous-boot proof")
			}
			if actions != "" {
				if data, err := os.ReadFile(actions); err == nil && len(data) != 0 {
					t.Fatal("previous-boot recovery mutated a current unit")
				}
			}
			if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, "finalization")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("uncertain previous-boot data was frozen", err)
			}
		})
	}
}

func TestHelperFinalizationRebootStopAndInterruptedCleanup(t *testing.T) {
	for _, clean := range []bool{false, true} {
		t.Run(map[bool]string{false: "unclean", true: "retained_clean"}[clean], func(t *testing.T) {
			engine, _, spec := previousBootSkillFixture(t, "started")
			bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if clean {
				_, err := skillmanager.FinalizeWorkTreeWithPolicy(context.Background(), bundle, session.Snapshot.Binding, false, skillmanager.CopyPolicy{ReservePercent: 101})
				if err == nil {
					t.Fatal("capture fixture did not stop after retaining original clean termination")
				}
			}
			result, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID})
			if err != nil || result["skill_unclean"] != !clean || result["state_pending"] != true {
				t.Fatal("manual stop lost previous-boot data classification", result, err)
			}
			record, err := skillmanager.ReadFinalization(bundle, session.Snapshot.Binding)
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			target := "detached"
			if clean {
				target = "published"
			}
			ack, err := client.AcknowledgeSkillFinalization(context.Background(), "saved", helperFinalizationAck(record, target))
			if err != nil {
				t.Fatal(err)
			}
			// Model a crash after transient removal but before retaining its completion marker.
			if err := os.RemoveAll(spec.SessionRoot); err != nil {
				t.Fatal(err)
			}
			if err := bundle.RemoveAll("work"); err != nil {
				t.Fatal(err)
			}
			result, err = engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID})
			if err != nil || result["state_pending"] != false {
				t.Fatal("interrupted previous-boot cleanup did not converge", result, err)
			}
			if _, err := client.CleanupFinalizedSkillSession(context.Background(), "replay", ack); err != nil {
				t.Fatal(err)
			}
			if _, _, err := client.ReadSkillFinalization(context.Background(), "retained", record.Binding); err != nil {
				t.Fatal("reboot cleanup lost frozen content", err)
			}
			if err := os.Mkdir(spec.SessionRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := client.CleanupFinalizedSkillSession(context.Background(), "replacement", ack); err == nil {
				t.Fatal("cleanup replay removed a new transient root")
			}
		})
	}
}
