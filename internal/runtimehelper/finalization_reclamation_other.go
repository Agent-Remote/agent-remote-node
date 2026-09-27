//go:build !linux

package runtimehelper

import (
	"context"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (s *Server) reclaimFinalization(context.Context, skillmanager.FinalizationRecord, finalizationReclamationAuthority) error {
	return errReclamationPending
}

func (s *Server) resumeFinalizationReclamation(context.Context, string, string) (skillmanager.FinalizationRecord, error) {
	return skillmanager.FinalizationRecord{}, errReclamationPending
}
