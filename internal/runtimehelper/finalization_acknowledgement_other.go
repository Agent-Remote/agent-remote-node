//go:build !linux

package runtimehelper

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) acknowledgeFinalization(context.Context, Request) (skillmanager.FinalizationRecord, error) {
	return skillmanager.FinalizationRecord{}, errors.New("finalization acknowledgement requires Linux")
}

func (e Engine) cleanupFinalization(context.Context, Request) (skillmanager.FinalizationRecord, error) {
	return skillmanager.FinalizationRecord{}, errors.New("finalization cleanup requires Linux")
}
