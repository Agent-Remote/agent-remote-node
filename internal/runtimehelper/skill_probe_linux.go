package runtimehelper

import "github.com/Agent-Remote/agent-remote-node/internal/skillmanager"

func (e Engine) probeSkillStorage() error {
	store, err := e.openSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	return skillmanager.ProbeStateStore(store, e.config.WithDefaults().SkillStatePolicy)
}
