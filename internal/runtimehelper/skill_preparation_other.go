//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) prepareTransferredSkillSnapshot(_ context.Context, _ skillmanager.SkillSnapshot, _ func(context.Context, string) (io.ReadCloser, error)) error {
	return errors.New("skill preparation requires the Linux helper")
}
