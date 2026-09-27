//go:build !linux

package runtimehelper

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) drainDeployment(_ context.Context, _ Request) (skillmanager.DeploymentDrain, error) {
	return skillmanager.DeploymentDrain{}, errors.New("deployment drain requires the Linux helper")
}
