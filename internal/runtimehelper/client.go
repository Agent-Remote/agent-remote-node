package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

const (
	// ProtocolVersion is the current local runtime-helper protocol version.
	ProtocolVersion            = 1
	maxHelperResponseBytes     = 1 << 20
	maxHelperDialResponseBytes = 8 << 10
	fileDescriptorTransferAck  = byte(0x06)
)

// Request is the versioned local runtime-helper request envelope.
type Request struct {
	Version   int            `json:"version"`
	RequestID string         `json:"request_id"`
	Operation string         `json:"operation"`
	Payload   map[string]any `json:"payload"`
}

// Response is the versioned local runtime-helper response envelope.
type Response struct {
	Version int            `json:"version"`
	OK      bool           `json:"ok"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *Error         `json:"error,omitempty"`
}

// Error is a non-sensitive runtime-helper error.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error preserves the helper's bounded stable error code for task reporting.
func (e *Error) Error() string { return fmt.Sprintf("runtime helper %s: %s", e.Code, e.Message) }

// DialSessionLoopbackPayload identifies one managed session loopback port.
type DialSessionLoopbackPayload struct {
	SessionID      string `json:"session_id"`
	RuntimeBackend string `json:"runtime_backend"`
	Port           int    `json:"port"`
}

// Client calls the root-owned helper over a Unix socket.
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient creates a runtime-helper client.
func NewClient(socketPath string) Client {
	return Client{socketPath: socketPath, timeout: 30 * time.Second}
}

// Call executes one declarative helper operation.
func (c Client) Call(ctx context.Context, requestID string, operation string, payload map[string]any) (map[string]any, error) {
	if requestID == "" || operation == "" {
		return nil, errors.New("runtime helper request_id and operation are required")
	}
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect runtime helper: %w", err)
	}
	defer connection.Close()
	if operation == deploymentDrainOperation || operation == migrationRecoveryOperation {
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer stop()
	}
	if operation == accountTakeoverOperation || operation == managedSpecOperation || operation == managedLaunchOperation || operation == managedRecoveryOperation || operation == managedCancelOperation || operation == skillReconciliationOperation || operation == skillAdmissionDrainOperation || operation == finalizationListOperation || operation == finalizationInspectOperation || operation == finalizationAckOperation || operation == finalizationCleanupOperation {
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer stop()
	}
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	request := Request{
		Version:   ProtocolVersion,
		RequestID: requestID,
		Operation: operation,
		Payload:   payload,
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nil, fmt.Errorf("encode runtime helper request: %w", err)
	}
	limit := int64(maxHelperResponseBytes)
	if operation == "import_account_config" {
		limit = maxHelperImportBytes
	}
	response, err := readHelperResponseLimitNumbers(connection, limit, operation == deploymentDrainOperation || operation == accountTakeoverOperation || operation == skillReconciliationOperation || operation == skillAdmissionDrainOperation || operation == finalizationListOperation || operation == finalizationInspectOperation || operation == finalizationAckOperation || operation == finalizationCleanupOperation)
	if err != nil {
		return nil, fmt.Errorf("decode runtime helper response: %w", err)
	}
	if response.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported runtime helper response version %d", response.Version)
	}
	if !response.OK {
		if response.Error == nil {
			return nil, errors.New("runtime helper returned an unspecified error")
		}
		return nil, response.Error
	}
	return response.Result, nil
}

// DialSessionLoopback asks the helper to connect inside a managed session network namespace.
func (c Client) DialSessionLoopback(ctx context.Context, requestID string, payload DialSessionLoopbackPayload) (net.Conn, error) {
	if requestID == "" {
		return nil, errors.New("runtime helper request_id is required")
	}
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect runtime helper: %w", err)
	}
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return nil, errors.New("runtime helper requires a Unix connection")
	}
	deadline := time.Now().Add(c.timeout)
	if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	mapped, err := Map(payload)
	if err != nil {
		return nil, err
	}
	request := Request{
		Version: ProtocolVersion, RequestID: requestID,
		Operation: "dial_session_loopback", Payload: mapped,
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nil, fmt.Errorf("encode runtime helper dial request: %w", err)
	}
	data := make([]byte, maxHelperDialResponseBytes+1)
	oob := make([]byte, syscall.CmsgSpace(4))
	dataLength, oobLength, flags, _, err := unixConnection.ReadMsgUnix(data, oob)
	if err != nil {
		return nil, fmt.Errorf("read runtime helper dial response: %w", err)
	}
	fileDescriptors, err := parseFileDescriptors(oob[:oobLength])
	if err != nil {
		return nil, err
	}
	defer func() { closeFileDescriptors(fileDescriptors) }()
	if dataLength == 0 || dataLength > maxHelperDialResponseBytes || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
		return nil, errors.New("runtime helper dial response is invalid")
	}
	response, err := decodeHelperResponse(data[:dataLength])
	if err != nil {
		return nil, fmt.Errorf("decode runtime helper dial response: %w", err)
	}
	if response.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported runtime helper response version %d", response.Version)
	}
	if !response.OK {
		if response.Error == nil {
			return nil, errors.New("runtime helper returned an unspecified error")
		}
		return nil, response.Error
	}
	if len(fileDescriptors) != 1 {
		return nil, fmt.Errorf("runtime helper returned %d file descriptors", len(fileDescriptors))
	}
	file := os.NewFile(uintptr(fileDescriptors[0]), "session-loopback")
	if file == nil {
		return nil, errors.New("runtime helper returned an invalid file descriptor")
	}
	fileDescriptors = nil
	forwardedConnection, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		return nil, fmt.Errorf("adopt runtime helper connection: %w", err)
	}
	if _, err := connection.Write([]byte{fileDescriptorTransferAck}); err != nil {
		_ = forwardedConnection.Close()
		return nil, fmt.Errorf("acknowledge runtime helper connection: %w", err)
	}
	return forwardedConnection, nil
}

func readHelperResponse(reader io.Reader) (Response, error) {
	return readHelperResponseLimit(reader, maxHelperResponseBytes)
}

func readHelperResponseLimit(reader io.Reader, limit int64) (Response, error) {
	return readHelperResponseLimitNumbers(reader, limit, false)
}

func readHelperResponseLimitNumbers(reader io.Reader, limit int64, numbers bool) (Response, error) {
	bounded := bufio.NewReader(io.LimitReader(reader, limit+1))
	data, err := bounded.ReadBytes('\n')
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return Response{}, errors.New("runtime helper response is invalid")
	}
	return decodeHelperResponseNumbers(data, numbers)
}

func decodeHelperResponse(data []byte) (Response, error) {
	return decodeHelperResponseNumbers(data, false)
}

func decodeHelperResponseNumbers(data []byte, numbers bool) (Response, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Response{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if numbers {
		decoder.UseNumber()
	}
	var response Response
	if err := decoder.Decode(&response); err != nil {
		return Response{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Response{}, errors.New("runtime helper response must contain one JSON object")
	}
	return response, nil
}

func parseFileDescriptors(oob []byte) ([]int, error) {
	messages, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("parse runtime helper control message: %w", err)
	}
	fileDescriptors := []int{}
	for _, message := range messages {
		rights, parseErr := syscall.ParseUnixRights(&message)
		if parseErr != nil {
			closeFileDescriptors(fileDescriptors)
			return nil, fmt.Errorf("parse runtime helper file descriptors: %w", parseErr)
		}
		fileDescriptors = append(fileDescriptors, rights...)
	}
	return fileDescriptors, nil
}

func closeFileDescriptors(fileDescriptors []int) {
	for _, fileDescriptor := range fileDescriptors {
		_ = syscall.Close(fileDescriptor)
	}
}

// Map converts a typed payload to a generic protocol payload.
func Map(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}
