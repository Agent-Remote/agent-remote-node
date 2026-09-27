//go:build !linux

package runtimehelper

import (
	"context"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) streamStoppedExport(context.Context, io.Writer, skillmanager.NodeExportBinding) error {
	return skillexport.ErrUnavailable
}

func (e Engine) streamStoppedSource(context.Context, io.Writer, skillmanager.NodeExportBinding, bool) error {
	return skillexport.ErrUnavailable
}
