//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) prepareTransferredDeployment(_ context.Context, _ skillmanager.SkillDeployment, _ func(context.Context, string) (io.ReadCloser, error)) (skillmanager.DeploymentPreparation, error) {
	return skillmanager.DeploymentPreparation{}, errors.New("deployment preparation requires the Linux helper")
}
