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

const deploymentDrainOperation = "drain_skill_deployment"

// DrainSkillDeployment permanently prevents preparation of an exact original deployment attempt.
// The receipt grants no Server terminal state, content deletion or session/process authority.
func (c Client) DrainSkillDeployment(ctx context.Context, requestID string, binding skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
	if err := binding.Validate(); err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	if err := validateID(requestID, "request_id"); err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	payload, err := Map(binding)
	if err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	result, err := c.Call(ctx, requestID, deploymentDrainOperation, payload)
	if err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	if len(result) != 1 || result["receipt"] == nil {
		return skillmanager.DeploymentDrain{}, errors.New("invalid deployment drain response")
	}
	data, err := json.Marshal(result["receipt"])
	if err != nil {
		return skillmanager.DeploymentDrain{}, err
	}
	var receipt skillmanager.DeploymentDrain
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, err
	}
	return receipt, receipt.Validate(binding)
}

func validateDeploymentDrainRequest(ctx context.Context, request Request, nodeID string) (skillmanager.SkillDeploymentIdentity, error) {
	var binding skillmanager.SkillDeploymentIdentity
	if err := ctx.Err(); err != nil {
		return binding, err
	}
	if request.Version != ProtocolVersion || request.Operation != deploymentDrainOperation || validateID(request.RequestID, "request_id") != nil || len(request.Payload) != 10 {
		return binding, errors.New("invalid deployment drain request")
	}
	for _, key := range []string{"operation_id", "attempt_id", "task_id", "user_id", "account_id", "node_id", "checkpoint_id", "plan_digest", "tree_digest", "runtime_backend"} {
		if request.Payload[key] == nil {
			return binding, errors.New("missing deployment drain binding")
		}
	}
	if err := decodeStrictPayload(request.Payload, &binding); err != nil {
		return binding, err
	}
	if err := binding.Validate(); err != nil {
		return binding, err
	}
	if binding.NodeID != nodeID {
		return binding, errors.New("deployment drain belongs to another Node")
	}
	return binding, nil
}

func (s *Server) handleDeploymentDrain(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = reader.ReadByte(); cancel() }()
	defer func() { _ = connection.Close(); <-done }()
	if err := s.lockSkillPreparation(ctx); err != nil {
		return
	}
	receipt, err := s.engine.drainDeployment(ctx, request)
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("DEPLOYMENT_DRAIN_PENDING", "Original deployment drain requires recovery."))
		return
	}
	result, err := Map(struct {
		Receipt skillmanager.DeploymentDrain `json:"receipt"`
	}{receipt})
	if err != nil || ctx.Err() != nil {
		return
	}
	_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}
