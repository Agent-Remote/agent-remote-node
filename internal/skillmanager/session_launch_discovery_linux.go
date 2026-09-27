package skillmanager

import (
	"errors"
	"os"
)

// ReadRetainedSessionLaunch discovers original launch authority from a verified retained bundle.
// Missing launch intent is distinct from missing authority within an existing launch record.
func ReadRetainedSessionLaunch(store *os.Root, session SessionSnapshot) (SessionLaunch, error) {
	if err := validateSessionSnapshot(session); err != nil {
		return SessionLaunch{}, err
	}
	binding := session.Snapshot.Binding
	identity := SkillSnapshotIdentity{SnapshotID: binding.SnapshotID, TaskID: binding.TaskID,
		NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID,
		SessionID: binding.SessionID, RuntimeBackend: session.Runtime.Backend}
	if identity.Validate() != nil || identity.RuntimeBackend != "native" {
		return SessionLaunch{}, errors.New("retained session lacks managed launch identity")
	}
	name := "session-launch-" + binding.SessionID + ".json"
	if _, err := store.Lstat(name); err != nil {
		return SessionLaunch{}, err
	}
	var launch SessionLaunch
	if err := readPrivateJSON(store, name, 1<<20, &launch); err != nil {
		return SessionLaunch{}, errors.New("retained launch intent is invalid")
	}
	if validateSessionLaunch(launch) != nil || launch.Spec.Identity != identity ||
		launch.BootID != session.Runtime.BootID || launch.UnitName != session.Runtime.ResourceID {
		return SessionLaunch{}, errors.New("retained launch differs from original session binding")
	}
	if err := requireLaunchSpec(store, launch.Spec); err != nil {
		return SessionLaunch{}, errors.New("retained launch lost its original spec authority")
	}
	return launch, nil
}

// ReadRetainedSessionSpecIntent discovers the original ready spec for a retained prepared bundle.
// Preparation can precede a launch intent; absence of a launch must not discard that authority.
func ReadRetainedSessionSpecIntent(store *os.Root, session SessionSnapshot) (SessionSpecIntent, error) {
	if err := validateSessionSnapshot(session); err != nil {
		return SessionSpecIntent{}, err
	}
	binding := session.Snapshot.Binding
	identity := SkillSnapshotIdentity{SnapshotID: binding.SnapshotID, TaskID: binding.TaskID,
		NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID,
		SessionID: binding.SessionID, RuntimeBackend: session.Runtime.Backend}
	var intent SessionSpecIntent
	if err := readPrivateJSON(store, "spec-intent-"+binding.SessionID+".json", 1<<20, &intent); err != nil {
		return intent, err
	}
	if validateSessionSpecIntent(intent) != nil || intent.Identity != identity || intent.State != "ready" {
		return intent, errors.New("retained preparation lost its original ready spec")
	}
	_, err := ReadSessionSpecDraft(store, intent)
	return intent, err
}
