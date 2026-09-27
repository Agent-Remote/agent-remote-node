package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Every stop path uses the retained invocation, including missing transient specs and cancelled
// recovery. The non-invocation sentinel permits absence but can never match a loaded service.
func (e Engine) managedNativeStopAuthority(ctx context.Context, supplied SessionSpec) (SessionSpec, string, error) {
	if supplied.SkillSnapshotID == "" {
		return supplied, "", nil
	}
	bundle, session, err := e.retainedNativeSkillSession(supplied.SessionID)
	if err != nil {
		return supplied, "", err
	}
	defer bundle.Close()
	if session.Snapshot.Binding.TaskID == "" {
		// Pre-streaming internal bundles have no managed launch authority to discover.
		return supplied, "", nil
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return supplied, "", err
	}
	defer store.Close()
	intent, err := skillmanager.ReadRetainedSessionSpecIntent(store, session)
	if err != nil {
		return supplied, "", err
	}
	spec, err := e.retainedSessionSpec(store, session, intent)
	if err != nil {
		return supplied, "", err
	}
	if spec.SkillSnapshotID != supplied.SkillSnapshotID || spec.UnitName != supplied.UnitName ||
		spec.BootID != supplied.BootID || spec.SessionRoot != filepath.Join(e.config.StateRoot, "sessions", spec.SessionID) ||
		!validSkillUUID(spec.BootID) || spec.BootID != currentBootID() {
		return supplied, "", errors.New("managed stop differs from original runtime authority")
	}
	if supplied.ManagedSkills != (ManagedSessionSpecBinding{}) {
		original, originalErr := managedSpecDigest(spec)
		provided, providedErr := managedSpecDigest(supplied)
		if originalErr != nil || providedErr != nil || original != provided {
			return supplied, "", errors.New("managed stop spec differs from original draft")
		}
	}
	launch, err := skillmanager.ReadRetainedSessionLaunch(store, session)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return supplied, "", err
	}
	missing := errors.Is(err, os.ErrNotExist)
	state, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil {
		return supplied, "", err
	}
	if !missing && launch.State == "starting" && state.LoadState != "not-found" {
		launch, err = e.observeStartingManagedInvocation(ctx, store, spec, launch, state)
		if err != nil {
			return supplied, "", err
		}
	}
	invocation := launch.InvocationID
	if invocation == "" {
		invocation = "absent"
	}
	if err := requireRetainedInvocation(state, spec, invocation); err != nil {
		return supplied, "", err
	}
	return spec, invocation, nil
}
