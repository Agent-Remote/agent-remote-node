package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) openSkillStateRoot() (*os.Root, error) {
	config := e.config.WithDefaults()
	if err := config.SkillStatePolicy.Validate(); err != nil {
		return nil, err
	}
	return skillmanager.OpenStateStore(config.SkillStateRoot,
		config.StateRoot, config.WorkspaceRoot, config.AccountRoot, config.BrowserRoot,
		config.EgoBrowserBrokerRoot,
	)
}

func (e Engine) openExistingSkillStateRoot() (*os.Root, error) {
	config := e.config.WithDefaults()
	if err := config.SkillStatePolicy.Validate(); err != nil {
		return nil, err
	}
	return skillmanager.OpenExistingStateStore(config.SkillStateRoot,
		config.StateRoot, config.WorkspaceRoot, config.AccountRoot, config.BrowserRoot,
		config.EgoBrowserBrokerRoot,
	)
}

// The stream adapter supplies validated content only for an existing matching trusted spec.
// Only the helper selects the execution identity, capture exclusions and filesystem location.
func (e Engine) prepareNativeSkillSnapshot(ctx context.Context, spec SessionSpec, binding skillmanager.SnapshotBinding, source skillmanager.Manifest, open skillmanager.ObjectOpener) error {
	if err := validateSessionSpec(e.config, spec, e.specPath(spec.SessionID)); err != nil {
		return err
	}
	if err := e.validateNativeSkillBinding(spec, binding); err != nil {
		return err
	}
	if binding.SnapshotID != spec.SkillSnapshotID {
		return errors.New("skill snapshot must be recorded in the session spec before preparation")
	}
	if err := verifySkillSystemPins(spec, binding.SystemReleases); err != nil {
		return err
	}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	dependencies, err := nativeSkillPreparationDependencies(ctx, store, spec)
	if err != nil {
		return err
	}
	policy := e.config.WithDefaults().SkillStatePolicy
	options := skillmanager.MaterializeOptions{
		UID: spec.RuntimeUID, GID: spec.RuntimeGID,
		RuntimeDependencies: dependencies,
		Policy:              skillmanager.CopyPolicy{DirectoryBytes: policy.DirectoryBytes, Entries: policy.Entries, MinimumFreeBytes: policy.MinimumFreeBytes, ReservePercent: policy.ReservePercent},
	}
	receipt := skillmanager.SessionSnapshot{
		Snapshot: skillmanager.PreparedSnapshot{Version: 1, Binding: binding, Capture: skillmanager.CaptureOptions{
			DirectoryBytes: policy.DirectoryBytes, Entries: policy.Entries, SystemPaths: []string{"ego-browser", "agent-remote-device"},
			RuntimeDependencies: dependencies,
		}},
		Runtime: skillmanager.RuntimeBinding{Backend: "native", ResourceID: spec.UnitName, BootID: spec.BootID, UID: spec.RuntimeUID, GID: spec.RuntimeGID},
	}
	return skillmanager.PrepareSessionSnapshot(ctx, store, source, open, receipt, options)
}

func nativeSkillPreparationDependencies(ctx context.Context, store *os.Root, spec SessionSpec) (map[string]string, error) {
	bundle, previous, err := skillmanager.OpenSessionSnapshot(store, spec.SessionID)
	if err == nil {
		_ = bundle.Close()
		// PrepareSessionSnapshot still compares every original binding and policy field. A retry
		// must not recapture the environment or lose learned work when optional tools change.
		return previous.Snapshot.Capture.RuntimeDependencies, ctx.Err()
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return discoverNativePython(ctx, spec.RuntimeUID, spec.RuntimeGID)
}

func (e Engine) validateNativeSkillBinding(spec SessionSpec, binding skillmanager.SnapshotBinding) error {
	if binding.NodeID != e.config.NodeID || binding.UserID != spec.UserID || binding.SessionID != spec.SessionID || spec.Kind != "session" ||
		spec.AccountPath != filepath.Join(e.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID) ||
		spec.SkillSnapshotID != "" && spec.SkillSnapshotID != binding.SnapshotID {
		return errors.New("skill snapshot does not belong to the managed node, account and session")
	}
	return nil
}

func (e Engine) retainedNativeSkillSession(sessionID string) (*os.Root, skillmanager.SessionSnapshot, error) {
	if !validSkillUUID(sessionID) {
		return nil, skillmanager.SessionSnapshot{}, os.ErrNotExist
	}
	// Legacy stops must not create an otherwise unused skill store.
	if _, err := os.Lstat(e.config.SkillStateRoot); err != nil {
		return nil, skillmanager.SessionSnapshot{}, err
	}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return nil, skillmanager.SessionSnapshot{}, err
	}
	defer store.Close()
	bundle, receipt, err := skillmanager.OpenSessionSnapshot(store, sessionID)
	if err != nil {
		return nil, skillmanager.SessionSnapshot{}, err
	}
	if receipt.Snapshot.Binding.NodeID != e.config.NodeID || receipt.Runtime.Backend != "native" ||
		receipt.Runtime.ResourceID != "agent-remote-session-"+shortDigest(sessionID, 12)+".service" {
		_ = bundle.Close()
		return nil, skillmanager.SessionSnapshot{}, errors.New("retained skill snapshot has a different node or runtime binding")
	}
	return bundle, receipt, nil
}

func (e Engine) finalizeNativeSkillSession(ctx context.Context, spec SessionSpec, termination nativeTermination) (map[string]any, error) {
	bundle, receipt, err := e.retainedNativeSkillSession(spec.SessionID)
	if errors.Is(err, os.ErrNotExist) && spec.SkillSnapshotID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("state_pending: retained skill snapshot cannot be opened; preserving session state")
	}
	defer bundle.Close()
	if err := e.validateNativeSkillBinding(spec, receipt.Snapshot.Binding); err != nil {
		return nil, err
	}
	if spec.BootID != receipt.Runtime.BootID || spec.RuntimeUID != receipt.Runtime.UID || spec.RuntimeGID != receipt.Runtime.GID {
		return nil, errors.New("skill snapshot runtime identity differs from the session spec")
	}
	record, err := skillmanager.FinalizeWorkTreeWithPolicy(ctx, bundle, receipt.Snapshot.Binding, termination.Unclean, e.finalizationCopyPolicy())
	if err != nil {
		return nil, err
	}
	if record.CanDeleteSession() {
		return nil, nil
	}
	return nativeSkillStopResult(receipt, record), nil
}

func (e Engine) stopRetainedNativeSkillSession(ctx context.Context, sessionID string) (map[string]any, error) {
	bundle, receipt, err := e.retainedNativeSkillSession(sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer bundle.Close()
	// Recovery uses only helper-owned retained identity; a missing spec never proves exit.
	termination, err := e.stopNativeWriters(ctx, SessionSpec{SessionID: sessionID, UnitName: receipt.Runtime.ResourceID, BootID: receipt.Runtime.BootID, SkillSnapshotID: receipt.Snapshot.Binding.SnapshotID})
	if err != nil {
		return nil, err
	}
	record, err := skillmanager.FinalizeWorkTreeWithPolicy(ctx, bundle, receipt.Snapshot.Binding, termination.Unclean, e.finalizationCopyPolicy())
	if err != nil {
		return nil, err
	}
	if record.CanDeleteSession() {
		recoverySpec := SessionSpec{
			SessionID: sessionID, SkillSnapshotID: receipt.Snapshot.Binding.SnapshotID,
			SessionRoot: filepath.Join(e.config.StateRoot, "sessions", sessionID),
		}
		_ = runCommand(ctx, e.config.IPPath, "netns", "delete", "ar-"+shortDigest(sessionID, 10))
		if err := e.cleanupTemp(ctx, recoverySpec); err != nil {
			return nil, err
		}
		if err := e.cleanupNativeSkillMount(recoverySpec); err != nil {
			return nil, err
		}
		if err := os.RemoveAll(recoverySpec.SessionRoot); err != nil {
			return nil, err
		}
	}
	return nativeSkillStopResult(receipt, record), nil
}

func nativeSkillStopResult(receipt skillmanager.SessionSnapshot, record skillmanager.FinalizationRecord) map[string]any {
	return map[string]any{
		"status": "stopped", "session_id": receipt.Snapshot.Binding.SessionID, "runtime_backend": "native",
		"runtime_resource_id": receipt.Runtime.ResourceID, "tmux_stopped": true,
		"skill_snapshot_id": receipt.Snapshot.Binding.SnapshotID, "skill_state": record.State, "skill_unclean": record.Unclean,
		"state_pending": !record.CanDeleteSession(),
	}
}

func (e Engine) finalizationCopyPolicy() skillmanager.CopyPolicy {
	policy := e.config.WithDefaults().SkillStatePolicy
	return skillmanager.CopyPolicy{DirectoryBytes: policy.DirectoryBytes, Entries: policy.Entries,
		MinimumFreeBytes: policy.MinimumFreeBytes, ReservePercent: policy.ReservePercent}
}
