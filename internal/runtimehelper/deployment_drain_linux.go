package runtimehelper

import (
	"context"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) drainDeployment(ctx context.Context, request Request) (skillmanager.DeploymentDrain, error) {
	binding, err := validateDeploymentDrainRequest(ctx, request, e.config.NodeID)
	if err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	defer store.Close()
	return skillmanager.DrainDeployment(ctx, store, binding)
}
