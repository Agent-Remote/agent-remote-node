package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const accountCaptureFileOperation = "open_account_capture_file"

// A legal 4096-byte path can expand sixfold when JSON escapes HTML characters.
const maxCaptureFileResponseBytes = 32 << 10
const maxCaptureManifestBytes = 64 << 20

var captureFileDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type accountCaptureFileRequest struct {
	Binding         skillmanager.AccountTakeoverBinding `json:"binding"`
	Kind            string                              `json:"kind"`
	Digest          string                              `json:"digest"`
	HelperReceiptID string                              `json:"helper_receipt_id"`
	TreeDigest      string                              `json:"tree_digest"`
}

type accountCaptureFileResponse struct {
	Capture skillmanager.AccountCapture `json:"capture"`
	Kind    string                      `json:"kind"`
	Size    int64                       `json:"size"`
	Entry   *skillmanager.Entry         `json:"entry"`
}

func (r accountCaptureFileRequest) validate() error {
	if err := r.Binding.Validate(); err != nil {
		return err
	}
	switch r.Kind {
	case "manifest":
		if r.Digest != "" || r.HelperReceiptID != "" || r.TreeDigest != "" {
			return errors.New("manifest request carries object identity")
		}
	case "object":
		capture := skillmanager.AccountCapture{Version: 1, Binding: r.Binding, HelperReceiptID: r.HelperReceiptID, TreeDigest: r.TreeDigest}
		if err := capture.Validate(); err != nil {
			return err
		}
		if !captureFileDigestPattern.MatchString(r.Digest) {
			return errors.New("invalid capture object digest")
		}
	default:
		return errors.New("invalid capture file kind")
	}
	return nil
}

func (s *Server) handleAccountCaptureFile(ctx context.Context, connection net.Conn, request Request) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return
	}
	s.mu.Lock()
	file, metadata, err := s.engine.openAccountCaptureFile(ctx, request)
	s.mu.Unlock()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("CAPTURE_UNAVAILABLE", "Retained account capture is unavailable for this binding."))
		return
	}
	defer file.Close()
	deadline := time.Now().Add(fileDescriptorAckTimeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = connection.SetDeadline(deadline)
	result, err := Map(metadata)
	if err != nil {
		return
	}
	data, err := json.Marshal(Response{Version: ProtocolVersion, OK: true, Result: result})
	if err != nil || len(data)+1 > maxCaptureFileResponseBytes {
		return
	}
	data = append(data, '\n')
	n, _, err := unixConnection.WriteMsgUnix(data, syscall.UnixRights(int(file.Fd())), nil)
	if err != nil || n != len(data) {
		return
	}
	var ack [1]byte
	if _, err := io.ReadFull(connection, ack[:]); err != nil || ack[0] != fileDescriptorTransferAck {
		return
	}
}

func validateCaptureFileRequest(ctx context.Context, request Request, nodeID string) (accountCaptureFileRequest, error) {
	if err := ctx.Err(); err != nil {
		return accountCaptureFileRequest{}, err
	}
	if request.Version != ProtocolVersion || request.Operation != accountCaptureFileOperation {
		return accountCaptureFileRequest{}, errors.New("invalid capture file operation")
	}
	if err := validateID(request.RequestID, "request_id"); err != nil {
		return accountCaptureFileRequest{}, err
	}
	var payload accountCaptureFileRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		return payload, err
	}
	if err := payload.validate(); err != nil {
		return payload, err
	}
	if payload.Binding.NodeID != nodeID {
		return payload, errors.New("capture belongs to another node")
	}
	return payload, nil
}

func captureFileMetadata(file *os.File, capture skillmanager.AccountCapture, kind string, entry *skillmanager.Entry) (accountCaptureFileResponse, error) {
	info, err := file.Stat()
	if err != nil {
		return accountCaptureFileResponse{}, err
	}
	return accountCaptureFileResponse{Capture: capture, Kind: kind, Size: info.Size(), Entry: entry}, nil
}
