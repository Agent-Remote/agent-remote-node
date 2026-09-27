package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func preparedManagedLaunch(t *testing.T) (Engine, ManagedSessionSpecRequest, SessionSpec, skillmanager.SessionLaunch) {
	t.Helper()
	return preparedManagedLaunchWithConfig(t, nil)
}

func preparedManagedLaunchWithConfig(t *testing.T, configure func(*Engine, *ManagedSessionSpecRequest)) (Engine, ManagedSessionSpecRequest, SessionSpec, skillmanager.SessionLaunch) {
	t.Helper()
	engine, input, snapshot := managedSpecFixture(t)
	engine.config.CgroupRoot = filepath.Join(filepath.Dir(engine.config.StateRoot), "cgroups")
	if err := os.Mkdir(engine.config.CgroupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(&engine, &input)
	}
	if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err != nil {
		t.Fatal(err)
	}
	spec, err := engine.loadSpec(input.Snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.prepareTransferredSkillSnapshot(context.Background(), snapshot, func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("original")), nil
	}); err != nil {
		t.Fatal(err)
	}
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ready, err := skillmanager.ReadSessionSpecIntent(store, engine.managedSpecIntent(spec))
	if err != nil {
		t.Fatal(err)
	}
	return engine, input, spec, skillmanager.SessionLaunch{Version: 1, Spec: ready, BootID: spec.BootID, UnitName: spec.UnitName, State: "starting"}
}

func TestManagedLaunchLinuxReplaysOriginalReceiptAfterTransientCleanup(t *testing.T) {
	engine, input, spec, launch := preparedManagedLaunch(t)
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
	if err := os.RemoveAll(spec.SessionRoot); err != nil {
		t.Fatal(err)
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	for range 2 {
		result, err := client.StartManagedSession(context.Background(), "original_managed_task", input)
		if err != nil || result["runtime_resource_id"] != spec.UnitName || result["status"] != "running" {
			t.Fatal("historical receipt did not replay", result, err)
		}
		if _, err := os.Lstat(spec.SessionRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("replay recreated transient files", err)
		}
	}
	input.Session.Argv = []string{"changed"}
	if _, err := client.StartManagedSession(context.Background(), "original_managed_task", input); err == nil {
		t.Fatal("changed launch input replayed")
	}
}

func TestManagedLaunchLinuxRecoveryBeforePreparationDoesNotCreateIntent(t *testing.T) {
	engine, input, _ := managedSpecFixture(t)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	for _, prepare := range []bool{false, true} {
		if prepare {
			if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err != nil {
				t.Fatal(err)
			}
		}
		result, err := client.RecoverManagedSession(context.Background(), "original_managed_task", input)
		if err != nil || result["status"] != "not_started" {
			t.Fatal("unstarted session could not be prepared", result, err)
		}
		if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+input.Snapshot.SessionID+".json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("recovery created launch intent", err)
		}
		if err := client.CancelManagedSession(context.Background(), "original_managed_task", input); err != nil {
			t.Fatal("cancellation of unstarted session mutated state", err)
		}
	}
}

func TestManagedLaunchLinuxRecoveryRejectsCorruptOrLinkedIntent(t *testing.T) {
	for _, kind := range []string{"corrupt", "dangling", "directory"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, _, _ := preparedManagedLaunch(t)
			file := filepath.Join(engine.config.SkillStateRoot, "session-launch-"+input.Snapshot.SessionID+".json")
			var err error
			switch kind {
			case "corrupt":
				err = os.WriteFile(file, []byte("{}"), 0o600)
			case "dangling":
				err = os.Symlink("missing", file)
			case "directory":
				err = os.Mkdir(file, 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			_, err = client.RecoverManagedSession(context.Background(), "original_managed_task", input)
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != "SKILL_START_PENDING" {
				t.Fatal("invalid intent permitted preparation", err)
			}
			if err := client.CancelManagedSession(context.Background(), "original_managed_task", input); err == nil {
				t.Fatal("invalid intent certified writer cleanup")
			}
		})
	}
}

func TestManagedLaunchLinuxMissingUnitFinalizesWithoutRelaunch(t *testing.T) {
	engine, input, spec, launch := preparedManagedLaunch(t)
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := skillmanager.BeginSessionLaunch(store, launch); err != nil {
		t.Fatal(err)
	}
	request := managedSpecRequest(t, input)
	request.Operation = managedLaunchOperation
	for range 2 {
		if _, err := engine.Execute(context.Background(), request); !errors.Is(err, errManagedStartStopped) {
			t.Fatal("ambiguous missing unit was not retained as stopped", err)
		}
	}
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	journal, err := skillmanager.ReadFinalization(bundle, receipt.Snapshot.Binding)
	if err != nil || !journal.Unclean {
		t.Fatal("unknown launch outcome became clean", err)
	}
	data, err := bundle.ReadFile("work/notes")
	if err != nil || string(data) != "original" {
		t.Fatal("ambiguous launch lost original work", err)
	}
	if saved, err := skillmanager.ReadSessionLaunch(store, launch); err != nil || !reflect.DeepEqual(saved, launch) {
		t.Fatal("ambiguous launch became successful", err)
	}
}

func TestManagedLaunchLinuxRequiresCompletedPreparation(t *testing.T) {
	engine, input, _ := managedSpecFixture(t)
	if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err != nil {
		t.Fatal(err)
	}
	request := managedSpecRequest(t, input)
	request.Operation = managedLaunchOperation
	if _, err := engine.Execute(context.Background(), request); err == nil {
		t.Fatal("unprepared snapshot launched")
	}
	path := filepath.Join(engine.config.SkillStateRoot, "session-launch-"+input.Snapshot.SessionID+".json")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unprepared snapshot gained launch intent", err)
	}
}

func TestManagedLaunchLinuxRecoveryDoesNotTreatHistoricalReadinessAsLiveness(t *testing.T) {
	engine, input, spec, launch := preparedManagedLaunch(t)
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
	if err := os.RemoveAll(spec.SessionRoot); err != nil {
		t.Fatal(err)
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	for range 2 {
		_, err := client.RecoverManagedSession(context.Background(), "original_managed_task", input)
		var failure *Error
		if !errors.As(err, &failure) || failure.Code != "SKILL_START_STOPPED" {
			t.Fatal("historical readiness became current liveness", err)
		}
	}
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if journal, err := skillmanager.ReadFinalization(bundle, receipt.Snapshot.Binding); err != nil || !journal.Unclean {
		t.Fatal("missing runtime lost unclean finalization", err)
	}
	if _, err := client.StartManagedSession(context.Background(), "original_managed_task", input); err != nil {
		t.Fatal("liveness check replaced historical launch outcome", err)
	}
	if _, err := os.Lstat(spec.SessionRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery recreated transient files", err)
	}
}
