package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) previousBootSkillSpec(store *os.Root, session skillmanager.SessionSnapshot) (SessionSpec, error) {
	intent, err := skillmanager.ReadRetainedSessionSpecIntent(store, session)
	if err != nil {
		return SessionSpec{}, err
	}
	// A missing launch is possible before systemd-run; an existing corrupt launch is never absence.
	if _, err := skillmanager.ReadRetainedSessionLaunch(store, session); err != nil && !errors.Is(err, os.ErrNotExist) {
		return SessionSpec{}, err
	}
	spec, err := e.retainedSessionSpec(store, session, intent)
	if err != nil {
		return spec, err
	}
	if spec.SessionRoot != filepath.Join(e.config.StateRoot, "sessions", spec.SessionID) ||
		spec.NetworkNamespace != "ar-"+shortDigest(spec.SessionID, 10) {
		return spec, errors.New("previous-boot runtime root differs from original configuration")
	}
	return spec, nil
}

func (e Engine) finalizePreviousBootSkillSession(ctx context.Context, store, bundle *os.Root, session skillmanager.SessionSnapshot, boot string) (skillmanager.FinalizationRecord, error) {
	spec, err := e.previousBootSkillSpec(store, session)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	for range 2 {
		if err := e.verifyPreviousBootSkillResources(ctx, session, spec, boot); err != nil {
			return skillmanager.FinalizationRecord{}, err
		}
	}
	if record, err := skillmanager.ReadFinalization(bundle, session.Snapshot.Binding); err == nil && record.ObjectsVersion == 1 {
		return record, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return skillmanager.FinalizationRecord{}, err
	}
	work, err := skillmanager.OpenSessionWork(bundle, session.Runtime)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	defer work.Close()
	// A previously retained termination remains immutable; otherwise reboot recovery is unclean.
	return skillmanager.FinalizeWorkTreeWithPolicy(ctx, bundle, session.Snapshot.Binding, true, e.finalizationCopyPolicy())
}

func (e Engine) recoverPreviousBootSkillStop(ctx context.Context, sessionID string) (map[string]any, error) {
	bundle, session, err := e.retainedNativeSkillSession(sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	boot := currentBootID()
	if boot == session.Runtime.BootID {
		return nil, nil
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	record, err := e.finalizePreviousBootSkillSession(ctx, store, bundle, session, boot)
	if err != nil {
		return nil, err
	}
	if record.CanDeleteSession() {
		payload, err := Map(finalizationCleanupRequest{Capture: record})
		if err != nil {
			return nil, err
		}
		if _, err := e.cleanupFinalization(ctx, Request{Version: ProtocolVersion, RequestID: "previous-boot-stop", Operation: finalizationCleanupOperation, Payload: payload}); err != nil {
			return nil, err
		}
	}
	return nativeSkillStopResult(session, record), nil
}

func (e Engine) verifyPreviousBootSkillResources(ctx context.Context, session skillmanager.SessionSnapshot, spec SessionSpec, boot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validSkillUUID(boot) || !validSkillUUID(session.Runtime.BootID) || boot == session.Runtime.BootID || currentBootID() != boot {
		return errors.New("previous-boot recovery requires a stable distinct kernel boot")
	}
	state, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, session.Runtime.ResourceID)
	if err != nil || state.LoadState != "not-found" || requireRetainedInvocation(state, spec, "") != nil {
		return errors.New("previous-boot runtime unit is present or uncertain")
	}
	groups, err := os.OpenRoot(e.config.CgroupRoot)
	if err != nil {
		return errors.New("previous-boot control groups cannot be inspected")
	}
	_, err = groups.Lstat(filepath.Join("system.slice", session.Runtime.ResourceID))
	_ = groups.Close()
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("previous-boot runtime cgroup is present or uncertain")
	}
	present, err := e.finalizedNetworkPresent(ctx, spec.NetworkNamespace)
	if err != nil || present {
		return errors.New("previous-boot runtime network is present or uncertain")
	}
	if err := verifyRetainedSpecFile(spec); err != nil {
		return err
	}
	workPath := filepath.Join(e.config.SkillStateRoot, "session-"+spec.SessionID, "work")
	if err := requireNoRebootMounts(ctx, spec.SessionRoot, workPath); err != nil {
		return err
	}
	if currentBootID() != boot {
		return errors.New("kernel boot changed during recovery")
	}
	return ctx.Err()
}

func (e Engine) cleanupPreviousBootSkillSession(ctx context.Context, bundle *os.Root, session skillmanager.SessionSnapshot, capture skillmanager.FinalizationRecord, root, boot string) error {
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	spec, err := e.previousBootSkillSpec(store, session)
	if err != nil {
		return err
	}
	for range 2 {
		if err := e.verifyPreviousBootSkillResources(ctx, session, spec, boot); err != nil {
			return err
		}
	}
	// No prior kernel mounts or namespaces can survive reboot. Reappearing ones are never removed.
	if err := removeFinalizedRuntimeRoot(root); err != nil {
		return err
	}
	return skillmanager.RetainFinalizationRuntimeCleanup(bundle, capture, root)
}
