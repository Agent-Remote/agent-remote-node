package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillDeploymentDownloader verifies one original deployment object before acknowledging its stream.
type SkillDeploymentDownloader func(context.Context, skillmanager.Entry, io.Writer) error

// PrepareSkillDeployment retains a complete original input through the independent Helper operation.
// The caller must maintain Server lease authority; a local receipt does not mark the operation ready.
func (c Client) PrepareSkillDeployment(ctx context.Context, requestID string, input skillmanager.SkillDeployment, download SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
	if validateID(requestID, "request_id") != nil || download == nil {
		return skillmanager.DeploymentPreparation{}, errors.New("invalid deployment preparation caller")
	}
	if err := input.Validate(input.SkillDeploymentIdentity); err != nil {
		return skillmanager.DeploymentPreparation{}, err
	}
	data, err := json.Marshal(input)
	if err != nil || len(data) > maxSkillPreparationBytes {
		return skillmanager.DeploymentPreparation{}, errors.New("deployment preparation input exceeds its limit")
	}
	ctx, cancel := context.WithTimeout(ctx, skillPreparationTimeout)
	defer cancel()
	connection, err := (&net.Dialer{Timeout: c.timeout}).DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return skillmanager.DeploymentPreparation{}, err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	header, err := Map(deploymentPreparationHeader{AttemptID: input.AttemptID, TaskID: input.TaskID, InputSize: int64(len(data))})
	if err != nil {
		return skillmanager.DeploymentPreparation{}, err
	}
	if err := json.NewEncoder(connection).Encode(Request{Version: ProtocolVersion, RequestID: requestID, Operation: deploymentPreparationOperation, Payload: header}); err != nil {
		return skillmanager.DeploymentPreparation{}, err
	}
	reader := bufio.NewReader(connection)
	frame, err := readDeploymentFrame(reader)
	if err != nil || frame != (deploymentPreparationFrame{Version: ProtocolVersion, Kind: "input"}) {
		return skillmanager.DeploymentPreparation{}, deploymentPreparationError(ctx)
	}
	if _, err := connection.Write(data); err != nil {
		return skillmanager.DeploymentPreparation{}, deploymentPreparationError(ctx)
	}
	return streamDeploymentObjects(ctx, connection, reader, input, download)
}

func streamDeploymentObjects(ctx context.Context, connection net.Conn, reader *bufio.Reader, input skillmanager.SkillDeployment, download SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
	files := skillPreparationFiles(input.Manifest)
	remaining := make(map[string]int)
	for _, entry := range input.Manifest.Entries {
		if entry.Kind == "file" {
			remaining[entry.SHA256]++
		}
	}
	for {
		frame, err := readDeploymentFrame(reader)
		if err != nil {
			return skillmanager.DeploymentPreparation{}, deploymentPreparationError(ctx)
		}
		if frame.Kind == "prepared" && frame.Digest == "" && frame.Receipt != nil {
			if err := frame.Receipt.Validate(input); err != nil {
				return skillmanager.DeploymentPreparation{}, err
			}
			return *frame.Receipt, ctx.Err()
		}
		entry, found := files[frame.Digest]
		if !found || remaining[frame.Digest] == 0 || frame != (deploymentPreparationFrame{Version: ProtocolVersion, Kind: "object", Digest: frame.Digest}) {
			return skillmanager.DeploymentPreparation{}, deploymentPreparationError(ctx)
		}
		remaining[frame.Digest]--
		verifier, err := skillmanager.NewContentVerifier(entry)
		if err != nil {
			return skillmanager.DeploymentPreparation{}, err
		}
		if err := download(ctx, entry, io.MultiWriter(verifier, connection)); err != nil {
			return skillmanager.DeploymentPreparation{}, err
		}
		if err := verifier.Finish(); err != nil {
			return skillmanager.DeploymentPreparation{}, err
		}
		if _, err := connection.Write([]byte{skillObjectComplete}); err != nil {
			return skillmanager.DeploymentPreparation{}, deploymentPreparationError(ctx)
		}
	}
}

func deploymentPreparationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &Error{Code: "SKILL_DEPLOYMENT_UNAVAILABLE", Message: "The original deployment input could not be prepared."}
}
