package runtimehelper

import (
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) validateManagedStopIntent(request Request, identity sessionLifecycleIdentity) error {
	if !validSkillUUID(identity.session) {
		return nil
	}
	var cancellation *ManagedSessionSpecRequest
	store, err := e.openExistingSkillStateRoot()
	if errors.Is(err, skillmanager.ErrStateStoreAbsent) || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer store.Close()
	if request.Operation == managedCancelOperation {
		var input ManagedSessionSpecRequest
		if err := decodeStrictPayload(request.Payload, &input); err != nil {
			return err
		}
		cancellation = &input
		digest, err := managedSpecInputDigest(request.RequestID, input, e.config)
		if err != nil {
			return err
		}
		expected := skillmanager.SessionSpecIntent{Version: 1, Identity: input.Snapshot, InputDigest: digest, State: "started"}
		_, err = skillmanager.ReadSessionSpecIntent(store, expected)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	bundle, retained, err := skillmanager.OpenSessionSnapshot(store, identity.session)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer bundle.Close()
	binding := retained.Snapshot.Binding
	if identity.backend != "native" || binding.NodeID != e.config.NodeID || binding.SessionID != identity.session {
		return errors.New("session stop conflicts with retained original identity")
	}
	if cancellation != nil {
		input := cancellation.Snapshot
		if input.NodeID != binding.NodeID || input.UserID != binding.UserID || input.AccountID != binding.AccountID || input.SessionID != binding.SessionID || input.TaskID != binding.TaskID || input.SnapshotID != binding.SnapshotID || input.RuntimeBackend != "native" || cancellation.SnapshotInputDigest != binding.PreparationDigest {
			return errors.New("managed cancellation differs from retained snapshot binding")
		}
	}
	if raw, present := request.Payload["skill_finalization"]; present {
		pointer, ok := raw.(map[string]any)
		if !ok || len(pointer) != 4 || pointer["snapshot_id"] != binding.SnapshotID || pointer["task_id"] != binding.TaskID || pointer["user_id"] != binding.UserID || pointer["account_id"] != binding.AccountID {
			return errors.New("session stop conflicts with retained finalization identity")
		}
	}
	return nil
}
