package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) openFinalizationReader(ctx context.Context, request Request) (retainedObjectReader, *os.File, skillmanager.FinalizationRecord, error) {
	var input finalizationReaderRequest
	var empty skillmanager.FinalizationRecord
	if request.Version != ProtocolVersion || request.Operation != finalizationReaderOperation || validateID(request.RequestID, "request_id") != nil || decodeStrictPayload(request.Payload, &input) != nil || input.Capture.Validate() != nil || input.Capture.Binding.NodeID != e.config.NodeID || ctx.Err() != nil {
		return nil, nil, empty, errors.New("invalid finalization reader identity")
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, nil, empty, err
	}
	defer store.Close()
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, input.Capture.Binding.SessionID)
	if err != nil {
		return nil, nil, empty, err
	}
	defer bundle.Close()
	if session.Snapshot.Binding != input.Capture.Binding || session.Runtime.Backend != "native" || session.Runtime.ResourceID != "agent-remote-session-"+shortDigest(input.Capture.Binding.SessionID, 12)+".service" {
		return nil, nil, empty, errors.New("reader differs from original Native capture")
	}
	record, err := skillmanager.ReadFinalization(bundle, input.Capture.Binding)
	if err != nil || !skillmanager.SameFinalizationInput(record, input.Capture) {
		return nil, nil, empty, errors.New("reader capture changed")
	}
	reader, manifest, err := skillmanager.OpenFinalizationReader(bundle, record)
	if err == nil && ctx.Err() != nil {
		_ = reader.Close()
		return nil, nil, empty, ctx.Err()
	}
	return reader, manifest, record, err
}
