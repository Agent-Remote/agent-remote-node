package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) streamStoppedExport(ctx context.Context, output io.Writer, binding skillmanager.NodeExportBinding) error {
	return e.streamStoppedSource(ctx, output, binding, false)
}

func (e Engine) streamStoppedSource(ctx context.Context, output io.Writer, binding skillmanager.NodeExportBinding, recovery bool) error {
	scanCtx, scanCancel := context.WithTimeout(ctx, skillexport.ScanTimeout)
	defer scanCancel()
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, binding.SessionID)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if session.Snapshot.Binding.ExportBinding() != binding || session.Runtime.Backend != "native" ||
		session.Runtime.ResourceID != "agent-remote-session-"+shortDigest(binding.SessionID, 12)+".service" {
		return skillexport.ErrUnavailable
	}
	if _, err := bundle.Lstat("finalization"); !errors.Is(err, os.ErrNotExist) {
		return skillexport.ErrUnavailable
	}
	verifyWriters, err := e.stoppedExportWriters(scanCtx, store, bundle, session)
	if err != nil {
		return err
	}
	if recovery {
		return streamRecoverySource(ctx, scanCtx, output, bundle, session, verifyWriters)
	}
	source, err := skillmanager.OpenStoppedWorkExport(scanCtx, bundle, session, skillexport.MaxExpandedBytes, skillexport.MaxEntries)
	if err != nil {
		return err
	}
	defer source.Close()
	header, err := skillexport.SnapshotHeader(binding, source.Manifest(), source.Unclean())
	if err != nil {
		return err
	}
	if err := verifyWriters(scanCtx); err != nil {
		return err
	}
	verify := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, skillexport.ScanTimeout)
		defer cancel()
		if err := verifyWriters(ctx); err != nil {
			return err
		}
		if err := source.Verify(ctx); err != nil {
			return err
		}
		return verifyWriters(ctx)
	}
	scanCancel()
	return skillexport.WriteSnapshot(ctx, output, header, source.Open, verify)
}

func streamRecoverySource(ctx, scanCtx context.Context, output io.Writer, bundle *os.Root, session skillmanager.SessionSnapshot, verifyWriters func(context.Context) error) error {
	source, err := skillmanager.OpenStoppedWorkRecovery(scanCtx, bundle, session)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := verifyWriters(scanCtx); err != nil {
		return err
	}
	verify := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, skillexport.ScanTimeout)
		defer cancel()
		if err := verifyWriters(ctx); err != nil {
			return err
		}
		if err := source.Verify(ctx); err != nil {
			return err
		}
		return verifyWriters(ctx)
	}
	return skillexport.WriteRecovery(ctx, output, session.Snapshot.Binding.ExportBinding(), source, verify)
}

// No invocation observation, service release or reconciliation is permitted on this read path.
func (e Engine) stoppedExportWriters(ctx context.Context, store, bundle *os.Root, session skillmanager.SessionSnapshot) (func(context.Context) error, error) {
	boot := currentBootID()
	if !validSkillUUID(boot) {
		return nil, skillexport.ErrUnavailable
	}
	spec, err := e.previousBootSkillSpec(store, session)
	if err != nil {
		return nil, err
	}
	var verify func(context.Context) error
	if boot != session.Runtime.BootID {
		verify = func(ctx context.Context) error {
			return e.verifyPreviousBootSkillResources(ctx, session, spec, boot)
		}
	} else {
		launch, err := skillmanager.ReadRetainedSessionLaunch(store, session)
		if err != nil {
			return nil, err
		}
		verify = func(ctx context.Context) error {
			if currentBootID() != boot {
				return skillexport.ErrUnavailable
			}
			current, err := skillmanager.ReadRetainedSessionLaunch(store, session)
			if err != nil || current != launch {
				return skillexport.ErrUnavailable
			}
			return e.verifyStoppedExportUnit(ctx, spec, launch.InvocationID)
		}
	}
	recheck := func(ctx context.Context) error {
		if err := requireStoppedExportEvidence(store, bundle, session); err != nil {
			return err
		}
		current, err := e.previousBootSkillSpec(store, session)
		if err != nil || !reflect.DeepEqual(current, spec) {
			return skillexport.ErrUnavailable
		}
		return verify(ctx)
	}
	if err := recheck(ctx); err != nil {
		return nil, err
	}
	return recheck, nil
}

// ENOSPC can prevent even a small termination receipt. Without it, require a recorded original
// invocation as well as passive writer quiescence; a ready-only or unobserved launch is insufficient.
func requireStoppedExportEvidence(store, bundle *os.Root, session skillmanager.SessionSnapshot) error {
	if _, err := bundle.Lstat("termination.json"); err == nil {
		_, err = skillmanager.ReadTermination(bundle, session.Snapshot.Binding)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	launch, err := skillmanager.ReadRetainedSessionLaunch(store, session)
	if err != nil || !validManagedInvocationID(launch.InvocationID) {
		return skillexport.ErrUnavailable
	}
	return nil
}

func (e Engine) verifyStoppedExportUnit(ctx context.Context, spec SessionSpec, invocation string) error {
	if err := verifyRetainedSpecFile(spec); err != nil {
		return err
	}
	state, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil || requireRetainedInvocation(state, spec, invocation) != nil {
		return skillexport.ErrUnavailable
	}
	if state.ActiveState != "inactive" && state.ActiveState != "failed" && !(state.ActiveState == "active" && state.SubState == "exited") {
		return skillexport.ErrUnavailable
	}
	if err := confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+spec.UnitName); err != nil {
		return err
	}
	after, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil || after != state {
		return skillexport.ErrUnavailable
	}
	return ctx.Err()
}
