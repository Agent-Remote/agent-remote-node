package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) reconcileSkillSession(ctx context.Context, request Request) (SkillSessionObservation, error) {
	input, err := validateSkillReconciliation(ctx, request, e.config.NodeID)
	result := SkillSessionObservation{SessionID: input.SessionID}
	if err != nil {
		return result, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return result, err
	}
	defer store.Close()
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, input.SessionID)
	if err != nil {
		return result, err
	}
	defer bundle.Close()
	if session.Snapshot.Binding.NodeID != input.NodeID || session.Runtime.Backend != "native" ||
		session.Runtime.ResourceID != "agent-remote-session-"+shortDigest(input.SessionID, 12)+".service" {
		return result, errors.New("retained skill runtime identity differs")
	}
	// Existing frozen input is immutable and readable without live runtime or draft authority.
	if _, err := bundle.Lstat("finalization"); err == nil {
		record, err := skillmanager.ReadFinalization(bundle, session.Snapshot.Binding)
		if err != nil {
			return result, err
		}
		result.State, result.Record = "finalized", &record
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	boot := currentBootID()
	if !validSkillUUID(boot) {
		return result, errors.New("current kernel boot identity is unavailable")
	}
	if session.Runtime.BootID != boot {
		record, err := e.finalizePreviousBootSkillSession(ctx, store, bundle, session, boot)
		if err != nil {
			return e.capturePendingObservation(ctx, store, bundle, session, err)
		}
		result.State, result.Record = "finalized", &record
		return result, nil
	}
	launch, err := skillmanager.ReadRetainedSessionLaunch(store, session)
	if errors.Is(err, os.ErrNotExist) {
		result.State = "not_started"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	spec, err := e.retainedSessionSpec(store, session, launch.Spec)
	if err != nil {
		return result, err
	}
	state, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil {
		return result, err
	}
	if launch.State == "starting" && state.LoadState != "not-found" {
		launch, err = e.observeStartingManagedInvocation(ctx, store, spec, launch, state)
		if err != nil {
			return result, err
		}
	}
	if err := requireRetainedInvocation(state, spec, launch.InvocationID); err != nil {
		return result, err
	}
	if state.ActiveState == "active" && state.SubState == "running" {
		if request.Operation == skillAdmissionDrainOperation && spec.EgoBrowserEnabled {
			termination, err := e.stopNativeWritersWithInvocation(ctx, spec, launch.InvocationID)
			if err != nil {
				return result, err
			}
			record, err := skillmanager.FinalizeWorkTreeWithPolicy(ctx, bundle, session.Snapshot.Binding, termination.Unclean, e.finalizationCopyPolicy())
			if err != nil {
				return e.capturePendingObservation(ctx, store, bundle, session, err)
			}
			result.State, result.Record = "finalized", &record
			return result, nil
		}
		if err := e.requireManagedSpecReceipt(spec); err != nil {
			return result, err
		}
		if err := e.verifyExistingManagedMount(spec); err != nil {
			return result, err
		}
		result.State = "running"
		return result, nil
	}
	if state.ActiveState != "inactive" && state.ActiveState != "failed" && !(state.ActiveState == "active" && state.SubState == "exited") {
		return result, errors.New("original runtime has not completed exit")
	}
	if err := confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+spec.UnitName); err != nil {
		return result, err
	}
	// Recheck the complete observation before releasing an exited RemainAfterExit service.
	// A populated or changing unit stays pending; background reconciliation never drains it.
	after, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil || state != after {
		return result, errors.New("original runtime changed during exit inspection")
	}
	if err := e.releaseExitedManagedUnit(ctx, spec, launch.InvocationID, state); err != nil {
		return result, err
	}
	record, err := skillmanager.FinalizeWorkTreeWithPolicy(ctx, bundle, session.Snapshot.Binding, !successfulNativeExit(state), e.finalizationCopyPolicy())
	if err != nil {
		return e.capturePendingObservation(ctx, store, bundle, session, err)
	}
	result.State, result.Record = "finalized", &record
	return result, nil
}

func (e Engine) releaseExitedManagedUnit(ctx context.Context, spec SessionSpec, invocation string, before nativeUnitState) error {
	if before.LoadState != "not-found" {
		command := exec.CommandContext(ctx, e.config.SystemctlPath, "stop", spec.UnitName)
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if err := command.Run(); err != nil {
			return errors.New("exited managed unit could not be released")
		}
	}
	after, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil {
		return err
	}
	if err := requireRetainedInvocation(after, spec, invocation); err != nil {
		return err
	}
	if after.ActiveState != "inactive" && after.ActiveState != "failed" {
		return errors.New("managed unit remains active after exit")
	}
	if err := confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+spec.UnitName); err != nil {
		return err
	}
	return ctx.Err()
}

func (e Engine) retainedSessionSpec(store *os.Root, session skillmanager.SessionSnapshot, intent skillmanager.SessionSpecIntent) (SessionSpec, error) {
	var spec SessionSpec
	draft, err := skillmanager.ReadSessionSpecDraft(store, intent)
	if err != nil {
		return spec, err
	}
	if err := decodeStrictJSON(draft, &spec); err != nil {
		return spec, err
	}
	if spec.ManagedSkills == (ManagedSessionSpecBinding{}) {
		return spec, errors.New("retained launch lacks managed spec identity")
	}
	expected := intent
	expected.State, expected.SpecDigest = "started", ""
	if e.managedSpecIntent(spec) != expected || e.validateNativeSkillBinding(spec, session.Snapshot.Binding) != nil ||
		spec.ManagedSkills.SnapshotInputDigest != session.Snapshot.Binding.PreparationDigest || spec.BootID != session.Runtime.BootID ||
		spec.UnitName != session.Runtime.ResourceID || spec.RuntimeUID != session.Runtime.UID || spec.RuntimeGID != session.Runtime.GID {
		return spec, errors.New("retained launch spec differs from prepared runtime")
	}
	return spec, nil
}
