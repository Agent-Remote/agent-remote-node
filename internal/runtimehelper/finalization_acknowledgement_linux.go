package runtimehelper

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) acknowledgeFinalization(ctx context.Context, request Request) (skillmanager.FinalizationRecord, error) {
	ack, err := validateFinalizationAcknowledgement(ctx, request, e.config.NodeID)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	bundle, session, err := e.retainedNativeSkillSession(ack.Capture.Binding.SessionID)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	defer bundle.Close()
	if session.Snapshot.Binding != ack.Capture.Binding {
		return skillmanager.FinalizationRecord{}, errors.New("acknowledgement differs from retained session")
	}
	return skillmanager.AcknowledgeFinalization(ctx, bundle, ack)
}
