package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const deploymentPreparationOperation = "prepare_skill_deployment"

type deploymentPreparationHeader struct {
	AttemptID string `json:"attempt_id"`
	TaskID    string `json:"task_id"`
	InputSize int64  `json:"input_size"`
}

type deploymentPreparationFrame struct {
	Version int                                 `json:"version"`
	Kind    string                              `json:"kind"`
	Digest  string                              `json:"digest,omitempty"`
	Receipt *skillmanager.DeploymentPreparation `json:"receipt,omitempty"`
}

func readDeploymentFrame(reader *bufio.Reader) (deploymentPreparationFrame, error) {
	data, err := readBoundedLine(reader, 4096)
	if err != nil {
		return deploymentPreparationFrame{}, err
	}
	var frame deploymentPreparationFrame
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return frame, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return frame, err
	}
	if fields["version"] == nil || fields["kind"] == nil {
		return frame, errors.New("missing deployment frame identity")
	}
	for key, value := range fields {
		if key != "version" && key != "kind" && key != "digest" && key != "receipt" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return frame, errors.New("invalid deployment frame field")
		}
	}
	if err := decodeStrictJSON(data, &frame); err != nil {
		return frame, err
	}
	if frame.Version != ProtocolVersion {
		return frame, errors.New("invalid deployment frame version")
	}
	return frame, nil
}

func validateDeploymentPreparationRequest(request Request) (deploymentPreparationHeader, error) {
	var header deploymentPreparationHeader
	if request.Version != ProtocolVersion || request.Operation != deploymentPreparationOperation || validateID(request.RequestID, "request_id") != nil || len(request.Payload) != 3 {
		return header, errors.New("invalid deployment preparation request")
	}
	for _, name := range []string{"attempt_id", "task_id", "input_size"} {
		if request.Payload[name] == nil {
			return header, errors.New("missing deployment preparation field")
		}
	}
	if err := decodeStrictPayload(request.Payload, &header); err != nil {
		return header, err
	}
	if !validSkillUUID(header.AttemptID) || !validSkillUUID(header.TaskID) || header.InputSize <= 0 || header.InputSize > maxSkillPreparationBytes {
		return header, errors.New("invalid deployment preparation identity or size")
	}
	return header, nil
}

func (s *Server) handleDeploymentPreparation(ctx context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	encoder := json.NewEncoder(connection)
	fail := func() { _ = encoder.Encode(deploymentPreparationFrame{Version: ProtocolVersion, Kind: "failed"}) }
	header, err := validateDeploymentPreparationRequest(request)
	if err != nil {
		fail()
		return
	}
	withSkillPreparationStream(ctx, connection, reader, func(ctx context.Context, stream io.Reader) {
		if err := encoder.Encode(deploymentPreparationFrame{Version: ProtocolVersion, Kind: "input"}); err != nil {
			return
		}
		data := make([]byte, header.InputSize)
		if _, err := io.ReadFull(stream, data); err != nil {
			fail()
			return
		}
		var input skillmanager.SkillDeployment
		if err := json.Unmarshal(data, &input); err != nil || input.AttemptID != header.AttemptID || input.TaskID != header.TaskID {
			fail()
			return
		}
		if err := input.Validate(input.SkillDeploymentIdentity); err != nil {
			fail()
			return
		}
		objects := &skillPreparationObjects{input: stream, encoder: encoder, files: skillPreparationFiles(input.Manifest)}
		if err := s.lockSkillPreparation(ctx); err != nil {
			fail()
			return
		}
		receipt, err := s.engine.prepareTransferredDeployment(ctx, input, objects.open)
		s.mu.Unlock()
		if err != nil || ctx.Err() != nil {
			fail()
			return
		}
		_ = encoder.Encode(deploymentPreparationFrame{Version: ProtocolVersion, Kind: "prepared", Receipt: &receipt})
	})
}
