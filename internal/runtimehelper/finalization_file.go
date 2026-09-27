package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const finalizationFileOperation = "open_skill_finalization_file"

type finalizationFileRequest struct {
	Binding    skillmanager.SnapshotBinding `json:"binding"`
	Kind       string                       `json:"kind"`
	Digest     string                       `json:"digest"`
	TreeDigest string                       `json:"tree_digest"`
	Unclean    *bool                        `json:"unclean"`
}

type finalizationFileResponse struct {
	Record skillmanager.FinalizationRecord `json:"record"`
	Kind   string                          `json:"kind"`
	Size   int64                           `json:"size"`
	Entry  *skillmanager.Entry             `json:"entry"`
}

func (r *finalizationFileRequest) UnmarshalJSON(data []byte) error {
	type plain finalizationFileRequest
	var decoded plain
	if err := decodeFinalizationFileFields(data, &decoded, "unclean", "binding", "kind", "digest", "tree_digest", "unclean"); err != nil {
		return err
	}
	*r = finalizationFileRequest(decoded)
	return nil
}

func (r *finalizationFileResponse) UnmarshalJSON(data []byte) error {
	type plain finalizationFileResponse
	var decoded plain
	if err := decodeFinalizationFileFields(data, &decoded, "entry", "record", "kind", "size", "entry"); err != nil {
		return err
	}
	*r = finalizationFileResponse(decoded)
	return nil
}

func decodeFinalizationFileFields(data []byte, out any, nullable string, names ...string) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != len(names) {
		return errors.New("invalid finalization file fields")
	}
	for _, name := range names {
		if fields[name] == nil || name != nullable && bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing finalization file field")
		}
	}
	return decodeStrictJSON(data, out)
}

func (r finalizationFileRequest) validate() error {
	if err := r.Binding.Validate(); err != nil {
		return err
	}
	switch r.Kind {
	case "manifest", "hold":
		if r.Digest == "" && r.TreeDigest == "" && r.Unclean == nil {
			return nil
		}
	case "object":
		if captureFileDigestPattern.MatchString(r.Digest) && captureFileDigestPattern.MatchString(r.TreeDigest) && r.Unclean != nil {
			return nil
		}
	}
	return errors.New("invalid retained finalization file identity")
}

func (s *Server) handleFinalizationFile(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	acknowledged := make(chan bool, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ack, err := reader.ReadByte()
		valid := err == nil && ack == fileDescriptorTransferAck
		if !valid {
			cancel()
		}
		acknowledged <- valid
	}()
	defer func() { _ = connection.Close(); <-done }()
	if err := s.lockSkillPreparation(ctx); err != nil {
		return
	}
	file, metadata, err := s.engine.openFinalizationFile(ctx, request)
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("FINALIZATION_UNAVAILABLE", "Retained skill finalization is unavailable for this binding."))
		return
	}
	defer file.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return
	}
	result, err := Map(metadata)
	if err != nil {
		return
	}
	data, err := json.Marshal(Response{Version: ProtocolVersion, OK: true, Result: result})
	if err != nil || len(data)+1 > maxCaptureFileResponseBytes {
		return
	}
	data = append(data, '\n')
	_ = connection.SetDeadline(time.Now().Add(fileDescriptorAckTimeout))
	n, _, err := unixConnection.WriteMsgUnix(data, syscall.UnixRights(int(file.Fd())), nil)
	if err != nil || n != len(data) {
		return
	}
	select {
	case <-acknowledged:
	case <-ctx.Done():
	}
}

func validateFinalizationFileRequest(ctx context.Context, request Request, nodeID string) (finalizationFileRequest, error) {
	var payload finalizationFileRequest
	if err := ctx.Err(); err != nil {
		return payload, err
	}
	if request.Version != ProtocolVersion || request.Operation != finalizationFileOperation || validateID(request.RequestID, "request_id") != nil {
		return payload, errors.New("invalid finalization file operation")
	}
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		return payload, err
	}
	if err := payload.validate(); err != nil {
		return payload, err
	}
	if payload.Binding.NodeID != nodeID {
		return payload, errors.New("finalization belongs to another node")
	}
	return payload, nil
}

func finalizationFileMetadata(file *os.File, record skillmanager.FinalizationRecord, kind string, entry *skillmanager.Entry) (finalizationFileResponse, error) {
	info, err := file.Stat()
	if err != nil {
		return finalizationFileResponse{}, err
	}
	return finalizationFileResponse{Record: record, Kind: kind, Size: info.Size(), Entry: entry}, nil
}
