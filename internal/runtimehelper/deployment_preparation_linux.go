package runtimehelper

import (
	"context"
	"errors"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) prepareTransferredDeployment(ctx context.Context, input skillmanager.SkillDeployment, open func(context.Context, string) (io.ReadCloser, error)) (skillmanager.DeploymentPreparation, error) {
	if err := input.Validate(input.SkillDeploymentIdentity); err != nil {
		return skillmanager.DeploymentPreparation{}, err
	}
	if input.NodeID != e.config.NodeID || input.RuntimeBackend != "native" {
		return skillmanager.DeploymentPreparation{}, errors.New("deployment preparation requires the original Native node")
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return skillmanager.DeploymentPreparation{}, err
	}
	defer store.Close()
	return skillmanager.PrepareDeployment(ctx, store, input, open, e.finalizationCopyPolicy())
}
