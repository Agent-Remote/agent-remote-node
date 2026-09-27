//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) openFinalizationReader(context.Context, Request) (retainedObjectReader, *os.File, skillmanager.FinalizationRecord, error) {
	return nil, nil, skillmanager.FinalizationRecord{}, errors.New("finalization reader requires Linux")
}
