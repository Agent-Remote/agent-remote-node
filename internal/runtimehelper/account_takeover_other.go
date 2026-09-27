//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) captureNativeAccountTakeover(context.Context, skillmanager.AccountTakeoverBinding, []skillmanager.AccountWriter) (skillmanager.AccountCapture, error) {
	return skillmanager.AccountCapture{}, errors.New("account takeover requires Linux")
}
