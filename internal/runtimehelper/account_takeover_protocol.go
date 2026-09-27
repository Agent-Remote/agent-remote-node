package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const accountTakeoverOperation = "capture_account_takeover"
const maxAccountTakeoverRequestBytes = 4 << 20
const accountTakeoverTimeout = 5 * time.Minute

type accountTakeoverRequest struct {
	Binding   skillmanager.AccountTakeoverBinding `json:"binding"`
	Inventory []skillmanager.AccountWriter        `json:"inventory"`
}

func (r accountTakeoverRequest) validate(nodeID string) error {
	digest, err := skillmanager.AccountInventoryDigest(r.Inventory)
	if err != nil || r.Binding.Validate() != nil || r.Binding.NodeID != nodeID || r.Binding.RuntimeBackend != "native" || digest != r.Binding.InventoryDigest {
		return errors.New("invalid Native account takeover identity")
	}
	for _, writer := range r.Inventory {
		if writer.NodeID != nodeID {
			return errors.New("account writer belongs to another node")
		}
	}
	return nil
}

// CaptureAccountTakeover freezes a stable Native source under its durable import fence.
// The worker must supply a freshly authorized reservation; no caller can select a host path.
func (c Client) CaptureAccountTakeover(ctx context.Context, requestID string, binding skillmanager.AccountTakeoverBinding, inventory []skillmanager.AccountWriter) (skillmanager.AccountCapture, error) {
	if inventory == nil {
		inventory = []skillmanager.AccountWriter{}
	}
	input := accountTakeoverRequest{binding, inventory}
	if err := input.validate(binding.NodeID); err != nil {
		return skillmanager.AccountCapture{}, err
	}
	payload, err := Map(input)
	if err != nil {
		return skillmanager.AccountCapture{}, err
	}
	c.timeout = accountTakeoverTimeout
	reply, err := c.Call(ctx, requestID, accountTakeoverOperation, payload)
	if err != nil {
		if ctx.Err() != nil {
			return skillmanager.AccountCapture{}, ctx.Err()
		}
		return skillmanager.AccountCapture{}, err
	}
	data, err := json.Marshal(reply)
	if err != nil {
		return skillmanager.AccountCapture{}, err
	}
	var capture skillmanager.AccountCapture
	if err := decodeFinalizationFileFields(data, &capture, "", "version", "binding", "helper_receipt_id", "tree_digest", "source_exists"); err != nil {
		return capture, err
	}
	if capture.Validate() != nil || capture.Binding != binding {
		return capture, errors.New("account capture response changed binding")
	}
	return capture, nil
}

func (s *Server) handleAccountTakeover(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, accountTakeoverTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = reader.ReadByte(); cancel() }()
	defer func() { _ = connection.Close(); <-done }()
	if err := s.lockSkillPreparation(ctx); err != nil {
		return
	}
	result, err := s.engine.Execute(ctx, request)
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("MIGRATION_PENDING", "Account takeover awaits verified writer quiescence and durable capture."))
		return
	}
	if ctx.Err() == nil {
		_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
	}
}

func (e Engine) captureAccountTakeover(ctx context.Context, request Request) (map[string]any, error) {
	var input accountTakeoverRequest
	if len(request.Payload) != 2 || request.Payload["binding"] == nil || request.Payload["inventory"] == nil {
		return nil, errors.New("invalid account takeover request")
	}
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return nil, err
	}
	if err := input.validate(e.config.NodeID); err != nil {
		return nil, err
	}
	capture, err := e.captureNativeAccountTakeover(ctx, input.Binding, input.Inventory)
	if err != nil {
		return nil, err
	}
	return Map(capture)
}
