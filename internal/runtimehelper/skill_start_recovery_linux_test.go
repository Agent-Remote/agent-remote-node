package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func beginReconciliationLaunch(t *testing.T, engine Engine, launch skillmanager.SessionLaunch) {
	t.Helper()
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := skillmanager.BeginSessionLaunch(store, launch); err != nil {
		t.Fatal(err)
	}
}

func TestHelperFinalizationStartingRecoversExitWithoutReadiness(t *testing.T) {
	for _, kind := range []string{"clean", "clean_missing_spec", "failed", "missing_unit", "missing_spec"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec, launch := preparedManagedLaunch(t)
			beginReconciliationLaunch(t, engine, launch)
			if kind != "missing_unit" && kind != "missing_spec" {
				engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, kind, strings.Repeat("a", 32))
			}
			if kind == "missing_spec" || kind == "clean_missing_spec" {
				if err := os.RemoveAll(spec.SessionRoot); err != nil {
					t.Fatal(err)
				}
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			observed, err := client.ReconcileSkillSession(context.Background(), "starting-exit", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || observed.State != "finalized" || observed.Record == nil || observed.Record.Unclean != (kind != "clean" && kind != "clean_missing_spec") {
				t.Fatal("starting exit was not retained", observed, err)
			}
			store, err := engine.openExistingSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			saved, err := skillmanager.ReadSessionLaunch(store, launch)
			if err != nil || saved.State == "started" {
				t.Fatal("exit observation invented historical readiness", err)
			}
			expected := "observed"
			if kind == "missing_unit" || kind == "missing_spec" {
				expected = "starting"
			}
			if saved.State != expected {
				t.Fatal("incorrect invocation observation", saved.State)
			}
			replay, err := client.ReconcileSkillSession(context.Background(), "retry", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || replay.Record == nil || *replay.Record != *observed.Record {
				t.Fatal("lost frozen replay", err)
			}
			if _, err := client.StartManagedSession(context.Background(), "original_managed_task", input); err == nil {
				t.Fatal("finalized starting runtime relaunched")
			}
		})
	}
}

func TestHelperFinalizationStartingRejectsUnprovenInvocation(t *testing.T) {
	for _, kind := range []string{"user", "transient", "group", "zero", "spec", "draft", "populated", "running_without_mount", "changing"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec, launch := preparedManagedLaunch(t)
			beginReconciliationLaunch(t, engine, launch)
			state := "clean"
			if kind == "running_without_mount" {
				state = "running"
			}
			command, actions := reconciliationSystemctl(t, spec, state, strings.Repeat("a", 32))
			engine.config.SystemctlPath = command
			data, err := os.ReadFile(command)
			if err != nil {
				t.Fatal(err)
			}
			body := string(data)
			switch kind {
			case "user":
				body = strings.ReplaceAll(body, "User="+spec.Username, "User=replacement")
			case "transient":
				body = strings.ReplaceAll(body, "Transient=yes", "Transient=no")
			case "group":
				body = strings.ReplaceAll(body, "ControlGroup=/system.slice/"+spec.UnitName, "ControlGroup=/other")
			case "zero":
				body = strings.ReplaceAll(body, strings.Repeat("a", 32), strings.Repeat("0", 32))
			case "spec":
				if err := os.WriteFile(engine.specPath(spec.SessionID), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "draft":
				if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json")); err != nil {
					t.Fatal(err)
				}
			case "populated":
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
				if err := os.MkdirAll(group, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "changing":
				marker := filepath.Join(t.TempDir(), "observed")
				body = strings.Replace(body, "case \"$1\" in", "if test -f '"+marker+"'; then printf 'LoadState=not-found\\nActiveState=inactive\\n'; exit 0; fi\ntouch '"+marker+"'\ncase \"$1\" in", 1)
			}
			if err := os.WriteFile(command, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			if result, err := client.ReconcileSkillSession(context.Background(), "unproven-start", input.Snapshot.NodeID, spec.SessionID); err == nil {
				t.Fatal("unproven invocation observed", result)
			}
			if _, err := os.Lstat(actions); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unproven invocation stopped", err)
			}
			if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, "finalization")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unproven work captured", err)
			}
		})
	}
}

func TestManagedObservedStopRejectsReplacementEvenWithoutSpec(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "present_spec", true: "missing_spec"}[missing], func(t *testing.T) {
			engine, input, spec, launch := preparedManagedLaunch(t)
			beginReconciliationLaunch(t, engine, launch)
			store, err := engine.openExistingSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := skillmanager.ObserveSessionLaunch(store, launch, strings.Repeat("a", 32)); err != nil {
				t.Fatal(err)
			}
			if missing {
				if err := os.RemoveAll(spec.SessionRoot); err != nil {
					t.Fatal(err)
				}
			}
			command, actions := reconciliationSystemctl(t, spec, "clean", strings.Repeat("b", 32))
			engine.config.SystemctlPath = command
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			if err := client.CancelManagedSession(context.Background(), "original_managed_task", input); err == nil {
				t.Fatal("cancellation accepted replacement")
			}
			if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID}); err == nil {
				t.Fatal("manual stop accepted replacement")
			}
			if _, err := os.Lstat(actions); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("replacement unit stopped", err)
			}
		})
	}
}

func TestManagedObservedStopCannotTurnAbsentLaunchIntoLoadedAuthority(t *testing.T) {
	for _, kind := range []string{"no_intent", "reappearing_after_absence"} {
		t.Run(kind, func(t *testing.T) {
			engine, _, spec, launch := preparedManagedLaunch(t)
			command, actions := reconciliationSystemctl(t, spec, "clean", strings.Repeat("a", 32))
			engine.config.SystemctlPath = command
			if kind == "reappearing_after_absence" {
				beginReconciliationLaunch(t, engine, launch)
				data, err := os.ReadFile(command)
				if err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(t.TempDir(), "first-observation")
				body := "if ! test -f '" + marker + "'; then touch '" + marker + "'; printf 'LoadState=not-found\\nActiveState=inactive\\n'; exit 0; fi\n"
				script := strings.Replace(string(data), "case \"$1\" in", body+"case \"$1\" in", 1)
				if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID}); err == nil {
				t.Fatal("absence authorized stopping a loaded unit")
			}
			if _, err := os.Lstat(actions); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unowned unit stopped", err)
			}
			if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, "finalization")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unowned unit became frozen", err)
			}
		})
	}
}
