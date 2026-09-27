//go:build !linux

package runtimehelper

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) listFinalizations(context.Context, Request) (skillmanager.FinalizationPage, error) {
	return skillmanager.FinalizationPage{}, errors.New("retained finalization inventory requires Linux")
}

func (e Engine) inspectFinalization(context.Context, Request) (skillmanager.FinalizationPage, error) {
	return skillmanager.FinalizationPage{}, errors.New("retained finalization inspection requires Linux")
}
