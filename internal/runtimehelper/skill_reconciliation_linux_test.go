package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func sealReconciliationLaunch(t *testing.T, engine Engine, launch skillmanager.SessionLaunch) {
	t.Helper()
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := skillmanager.BeginSessionLaunch(store, launch); err != nil {
		t.Fatal(err)
	}
	if err := skillmanager.FinishSessionLaunch(store, launch, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
}

func reconciliationSystemctl(t *testing.T, spec SessionSpec, state, invocation string) (string, string) {
	t.Helper()
	root := t.TempDir()
	log := filepath.Join(root, "actions")
	stopped := filepath.Join(root, "stopped")
	active, sub, result, code, status := "active", "exited", "success", "1", "0"
	if state == "failed" {
		active, sub, result, status = "failed", "failed", "signal", "9"
	}
	if state == "running" {
		sub, code = "running", "0"
	}
	group := "/system.slice/" + spec.UnitName
	if state == "no_group" {
		group = ""
	}
	output := fmt.Sprintf("LoadState=loaded\nActiveState=%s\nSubState=%s\nControlGroup=%s\nResult=%s\nExecMainCode=%s\nExecMainStatus=%s\nInvocationID=%s\nTransient=yes\nUser=%s\n", active, sub, group, result, code, status, invocation, spec.Username)
	body := fmt.Sprintf("case \"$1\" in\nstop) echo stop >> %q; touch %q; exit 0;;\nshow) ;;\n*) echo unexpected >> %q; exit 1;;\nesac\nif test -f %q; then printf 'LoadState=not-found\\nActiveState=inactive\\n'; else cat <<'STATE'\n%sSTATE\nfi", log, stopped, log, stopped, output)
	return writeTestCommand(t, "reconcile-systemctl", body), log
}

func TestHelperFinalizationReconcileOriginalExit(t *testing.T) {
	for _, kind := range []string{"clean", "no_group", "missing_spec", "missing_unit", "failed"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec, launch := preparedManagedLaunch(t)
			sealReconciliationLaunch(t, engine, launch)
			if kind != "missing_unit" {
				engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, kind, strings.Repeat("a", 32))
			}
			if kind == "missing_spec" {
				if err := os.RemoveAll(spec.SessionRoot); err != nil {
					t.Fatal(err)
				}
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			result, err := client.ReconcileSkillSession(context.Background(), "natural-exit", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || result.State != "finalized" || result.Record == nil {
				t.Fatal("original exit not captured", result, err)
			}
			if result.Record.Unclean != (kind == "missing_unit" || kind == "failed") {
				t.Fatal("incorrect termination classification", result.Record)
			}
			bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "original" {
				t.Fatal("lost original work", err)
			}
			// Frozen replay remains available after its launch draft and transient files disappear.
			if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json")); err != nil {
				t.Fatal(err)
			}
			replayed, err := client.ReconcileSkillSession(context.Background(), "after-restart", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || replayed.Record == nil || *replayed.Record != *result.Record {
				t.Fatal("frozen replay changed", replayed, err)
			}
			if _, err := skillmanager.ReadTermination(bundle, receipt.Snapshot.Binding); err != nil {
				t.Fatal("termination not retained", err)
			}
		})
	}
}

func TestHelperFinalizationReconcileDoesNotFreezeUncertainRuntime(t *testing.T) {
	for _, kind := range []string{"unstarted", "replacement", "populated", "missing_draft", "linked_launch", "wrong_node", "running_without_mount"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec, launch := preparedManagedLaunch(t)
			if kind != "unstarted" {
				sealReconciliationLaunch(t, engine, launch)
			}
			invocation := strings.Repeat("a", 32)
			if kind == "replacement" {
				invocation = strings.Repeat("b", 32)
			}
			state := "clean"
			if kind == "running_without_mount" {
				state = "running"
			}
			command, log := reconciliationSystemctl(t, spec, state, invocation)
			engine.config.SystemctlPath = command
			if kind == "populated" {
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
				if err := os.MkdirAll(group, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "missing_draft" {
				if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json")); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "linked_launch" {
				path := filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing", path); err != nil {
					t.Fatal(err)
				}
			}
			nodeID := input.Snapshot.NodeID
			if kind == "wrong_node" {
				nodeID = input.Snapshot.UserID
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			result, err := client.ReconcileSkillSession(context.Background(), "inspect-exit", nodeID, spec.SessionID)
			if kind == "unstarted" {
				if err != nil || result.State != "not_started" {
					t.Fatal("prepared session misclassified", result, err)
				}
			} else if err == nil {
				t.Fatal("uncertain runtime accepted", result)
			}
			if _, err := os.Lstat(log); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("reconciliation stopped an uncertain runtime", err)
			}
			for _, name := range []string{"finalization", "termination.json"} {
				if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("uncertain runtime frozen", name, err)
				}
			}
		})
	}
}

func TestHelperFinalizationReconcileRejectsContradictoryUnit(t *testing.T) {
	spec := SessionSpec{Username: "original", UnitName: "agent-remote-session-aaaaaaaaaaaa.service"}
	for _, state := range []nativeUnitState{
		{LoadState: "not-found", InvocationID: strings.Repeat("a", 32)},
		{LoadState: "not-found", User: "replacement"},
		{LoadState: "not-found", Transient: "yes"},
		{LoadState: "loaded", ActiveState: "active", SubState: "running", InvocationID: strings.Repeat("a", 32), Transient: "yes", User: spec.Username},
	} {
		if err := requireRetainedInvocation(state, spec, strings.Repeat("a", 32)); err == nil {
			t.Fatal("contradictory unit accepted", state)
		}
	}
}
