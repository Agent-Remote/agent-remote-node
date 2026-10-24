package runtimehelper

import (
	"context"
	"errors"
	"fmt"
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
	if err := engine.publishSessionStop(context.Background(), sessionStopIntent{Version: 1, NodeID: engine.config.NodeID, SessionID: spec.SessionID, Scope: "tool_session", RequestID: "original-stop", Operation: "stop_session", Backend: "native"}); err != nil {
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
	execution, err := client.StartManagedSessionExecution(context.Background(), "original_managed_task", input)
	if err != nil || execution.Result["status"] != "running" || !execution.Execution.ReceiptReplay || !execution.Execution.StopIntent || execution.Execution.FreshAdmission {
		t.Fatalf("historical managed result acquired admission: %+v %v", execution, err)
	}
	if _, err := client.RecoverManagedSessionExecution(context.Background(), "original_managed_task", input); err == nil {
		t.Fatal("closed session restored readiness")
	}
	input.Session.Argv = []string{"changed"}
	if _, err := client.StartManagedSession(context.Background(), "original_managed_task", input); err == nil {
		t.Fatal("changed launch input replayed")
	}
}

func TestManagedLaunchLinuxRecoveryBeforePreparationAndCancellationFence(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(fmt.Sprint(prepare), func(t *testing.T) {
			engine, input, _ := managedSpecFixture(t)
			if prepare {
				if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err != nil {
					t.Fatal(err)
				}
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			result, err := client.RecoverManagedSession(context.Background(), "original_managed_task", input)
			if err != nil || result["status"] != "not_started" {
				t.Fatal("unstarted session could not be prepared", result, err)
			}
			if err := client.CancelManagedSession(context.Background(), "original_managed_task", input); err == nil {
				t.Fatal("missing launch proved stopped")
			}
			if err := engine.requireSessionOpen(context.Background(), input.Snapshot.SessionID); !errors.Is(err, errSessionClosed) {
				t.Fatal("cancel did not persist intent", err)
			}
			if _, err := client.RecoverManagedSession(context.Background(), "original_managed_task", input); err == nil {
				t.Fatal("cancelled recovery authorized preparation")
			}
		})
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

func TestManagedCancelRetainedIdentityWithoutSpecIntent(t *testing.T) {
	engine, input, spec, _ := preparedManagedLaunch(t)
	if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-intent-"+spec.SessionID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(spec.SessionRoot); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"snapshot", "user", "account", "task", "digest"} {
		changed := input
		replacement := "88888888-8888-4888-8888-888888888888"
		switch field {
		case "snapshot":
			changed.Snapshot.SnapshotID = replacement
		case "user":
			changed.Snapshot.UserID = replacement
			changed.Session.UserID = replacement
		case "account":
			changed.Snapshot.AccountID = replacement
			changed.Session.ToolAccountID = replacement
		case "task":
			changed.Snapshot.TaskID = replacement
		case "digest":
			changed.SnapshotInputDigest = strings.Repeat("b", 64)
		}
		payload, err := Map(changed)
		if err != nil {
			t.Fatal(err)
		}
		request := Request{Version: 1, RequestID: "original_managed_task", Operation: managedCancelOperation, Payload: payload}
		if err := engine.prepareSessionStopIntents(context.Background(), request); err == nil {
			t.Fatal("changed retained identity closed session", field)
		}
		if err := engine.requireSessionOpen(context.Background(), spec.SessionID); err != nil {
			t.Fatal("rejected cancellation wrote marker", field, err)
		}
	}
}
