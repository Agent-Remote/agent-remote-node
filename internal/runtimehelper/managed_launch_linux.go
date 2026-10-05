package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) recoverManagedSession(ctx context.Context, request Request) (map[string]any, error) {
	var input ManagedSessionSpecRequest
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return nil, err
	}
	if err := input.validate(e.config.NodeID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, err
	}
	// Lstat treats even a dangling journal link as existing, never as permission to prepare again.
	_, lookupErr := store.Lstat("session-launch-" + input.Snapshot.SessionID + ".json")
	_ = store.Close()
	if errors.Is(lookupErr, os.ErrNotExist) {
		return map[string]any{
			"status": "not_started", "session_id": input.Snapshot.SessionID, "skill_snapshot_id": input.Snapshot.SnapshotID,
			"task_record_id": input.Snapshot.TaskID, "runtime_backend": "native",
		}, nil
	}
	if lookupErr != nil {
		return nil, lookupErr
	}
	// The serialized Helper mutation lock prevents a new intent between inspection and recovery.
	// Full original-input validation and all at-most-once rules remain in the launch implementation.
	return e.runManagedSession(ctx, request)
}

func (e Engine) startManagedSession(ctx context.Context, request Request) (map[string]any, error) {
	return e.runManagedSession(ctx, request)
}

func (e Engine) runManagedSession(ctx context.Context, request Request) (map[string]any, error) {
	var input ManagedSessionSpecRequest
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return nil, err
	}
	if err := input.validate(e.config.NodeID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest, err := managedSpecInputDigest(request.RequestID, input, e.config)
	if err != nil {
		return nil, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	expected := skillmanager.SessionSpecIntent{Version: 1, Identity: input.Snapshot, InputDigest: digest, State: "started"}
	ready, err := skillmanager.ReadSessionSpecIntent(store, expected)
	if err != nil || ready.State != "ready" {
		return nil, errManagedStartPending
	}
	draft, err := skillmanager.ReadSessionSpecDraft(store, ready)
	if err != nil {
		return nil, err
	}
	var spec SessionSpec
	if err := decodeStrictJSON(draft, &spec); err != nil || e.managedSpecIntent(spec) != expected || spec.ManagedSkills.SnapshotInputDigest != input.SnapshotInputDigest {
		return nil, errManagedStartPending
	}
	launch := skillmanager.SessionLaunch{Version: 1, Spec: ready, BootID: spec.BootID, UnitName: spec.UnitName, State: "starting"}
	saved, readErr := skillmanager.ReadSessionLaunch(store, launch)
	if readErr == nil && request.Operation == managedCancelOperation {
		if err := e.requireManagedLaunchBundle(spec); err != nil && !errors.Is(err, errManagedStartStopped) {
			return nil, err
		}
		if err := e.requireManagedCancellationUnit(ctx, spec, saved.InvocationID); err != nil {
			return nil, err
		}
		if _, err := e.stopSession(ctx, map[string]any{"session_id": spec.SessionID}); err != nil {
			return nil, err
		}
		return map[string]any{
			"status": "stopped", "session_id": input.Snapshot.SessionID, "skill_snapshot_id": input.Snapshot.SnapshotID,
			"task_record_id": input.Snapshot.TaskID, "runtime_backend": "native",
		}, nil
	}
	if readErr == nil && saved.State == "started" && request.Operation == managedLaunchOperation {
		// This is the historical task outcome, even if stop already removed the transient spec.
		return managedLaunchResult(spec), nil
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	if errors.Is(readErr, os.ErrNotExist) && request.Operation != managedLaunchOperation {
		return nil, errManagedStartPending
	}
	if readErr == nil {
		return e.recoverManagedLaunch(ctx, store, spec, launch, saved.InvocationID)
	}
	if err := e.requireManagedSpecReceipt(spec); err != nil {
		return nil, err
	}
	payload, err := Map(input.Session)
	if err != nil {
		return nil, err
	}
	if err := e.preflightManagedSpec(input, payload); err != nil {
		return nil, err
	}
	spec.EgoBrowserBrokerNonce = input.Session.EgoBrowserBrokerNonce
	if err := e.requireUnstartedManagedSpec(ctx, spec.SessionID); err != nil {
		return nil, err
	}
	if err := confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+spec.UnitName); err != nil {
		return nil, err
	}
	if err := e.requireManagedLaunchBundle(spec); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := skillmanager.BeginSessionLaunch(store, launch); err != nil {
		return nil, err
	}
	return e.launchManagedOnce(ctx, store, spec, launch)
}

func (e Engine) launchManagedOnce(ctx context.Context, store *os.Root, spec SessionSpec, launch skillmanager.SessionLaunch) (result map[string]any, err error) {
	defer func() {
		if err != nil || ctx.Err() != nil {
			result = nil
			err = errors.Join(errManagedStartPending, e.cleanupFailedNativeLaunch(spec))
		}
	}()
	if spec.DockerSocketPath != "" {
		if _, active := dockerCapabilityBrokers.Load(spec.DockerSocketPath); !active {
			if _, _, _, err := prepareDockerCapability(spec.SessionRoot, spec.SessionID, spec.WorkspacePath, e.config.DockerBinaryPath, spec.RuntimeUID, spec.RuntimeGID); err != nil {
				return nil, err
			}
		}
	}
	if err := e.launch(ctx, spec); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invocation, err := e.managedLaunchInvocation(ctx, spec)
	if err != nil {
		return nil, errManagedStartPending
	}
	if err := skillmanager.FinishSessionLaunch(store, launch, invocation); err != nil {
		return nil, err
	}
	return managedLaunchResult(spec), nil
}

func (e Engine) requireManagedLaunchBundle(spec SessionSpec) error {
	bundle, receipt, err := e.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if err := e.validateNativeSkillBinding(spec, receipt.Snapshot.Binding); err != nil {
		return err
	}
	if receipt.Snapshot.Binding.TaskID != spec.ManagedSkills.TaskID || receipt.Snapshot.Binding.PreparationDigest != spec.ManagedSkills.SnapshotInputDigest || receipt.Runtime.BootID != spec.BootID || receipt.Runtime.UID != spec.RuntimeUID || receipt.Runtime.GID != spec.RuntimeGID {
		return errManagedStartPending
	}
	if _, err := skillmanager.ReadFinalization(bundle, receipt.Snapshot.Binding); err == nil {
		return errManagedStartStopped
	} else if !errors.Is(err, os.ErrNotExist) {
		return errManagedStartPending
	}
	if _, err := skillmanager.ReadTermination(bundle, receipt.Snapshot.Binding); err == nil {
		return errManagedStartStopped
	} else if !errors.Is(err, os.ErrNotExist) {
		return errManagedStartPending
	}
	return nil
}

func (e Engine) recoverManagedLaunch(ctx context.Context, store *os.Root, spec SessionSpec, launch skillmanager.SessionLaunch, expectedInvocation string) (result map[string]any, err error) {
	defer func() {
		if ctx.Err() != nil {
			result = nil
			err = errors.Join(errManagedStartPending, e.cleanupFailedManagedRecovery(spec, expectedInvocation))
		}
	}()
	if err := e.requireManagedLaunchBundle(spec); err != nil {
		return nil, err
	}
	state, err := readNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil {
		return nil, errManagedStartPending
	}
	if state.LoadState == "loaded" && state.ActiveState == "active" && state.SubState == "running" {
		if err := e.requireManagedSpecReceipt(spec); err != nil {
			return nil, errManagedStartPending
		}
		if err := e.verifyExistingManagedMount(spec); err != nil {
			return nil, errManagedStartPending
		}
		before, err := e.managedLaunchInvocation(ctx, spec)
		if err != nil || expectedInvocation != "" && before != expectedInvocation {
			return nil, errManagedStartPending
		}
		if expectedInvocation == "" {
			if err := skillmanager.ObserveSessionLaunch(store, launch, before); err != nil {
				return nil, err
			}
			expectedInvocation = before
		}
		if err := e.waitForSessionReady(ctx, spec); err != nil {
			return nil, errManagedStartPending
		}
		after, err := e.managedLaunchInvocation(ctx, spec)
		if err != nil || before != after {
			return nil, errManagedStartPending
		}
		if err := skillmanager.FinishSessionLaunch(store, launch, before); err != nil {
			return nil, err
		}
		return managedLaunchResult(spec), nil
	}
	// Absence cannot prove that systemd-run never executed. Drain and retain the original
	// work through the common finalizer, without submitting another launch request.
	recoveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := e.stopSession(recoveryCtx, map[string]any{"session_id": spec.SessionID}); err != nil {
		return nil, errManagedStartPending
	}
	return nil, errManagedStartStopped
}

func managedLaunchResult(spec SessionSpec) map[string]any {
	return map[string]any{
		"status": "running", "session_id": spec.SessionID, "tool_account_id": filepath.Base(spec.AccountPath), "tool_type": "claude",
		"workspace_remote_path": spec.WorkspacePath, "account_remote_path": spec.AccountPath, "tmux_session_name": spec.TmuxSessionName,
		"sandbox_name": "", "container_id": "", "tmux_started": true, "runtime_backend": "native", "runtime_resource_id": spec.UnitName,
		"runtime_uid": spec.RuntimeUID, "skill_snapshot_id": spec.SkillSnapshotID, "task_record_id": spec.ManagedSkills.TaskID,
	}
}
