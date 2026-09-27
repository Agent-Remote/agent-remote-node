package runtimehelper

import (
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) checkAccountRuntimeFence(userID, accountID string) error {
	store, err := e.openSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	_, err = skillmanager.ReadAccountFence(store, e.config.NodeID, userID, accountID)
	if errors.Is(err, os.ErrNotExist) {
		if err := skillmanager.CheckAccountMigrationHistory(store, e.config.NodeID, userID, accountID); err != nil {
			return errMigrationWritersUnknown
		}
		return nil
	}
	if err != nil {
		return err
	}
	return errAccountMigrationPending
}
