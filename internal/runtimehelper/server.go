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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxHelperRequestBytes    = 1 << 20
	fileDescriptorAckTimeout = 5 * time.Second
)

// Server exposes the privileged engine only on a root-owned Unix socket.
type Server struct {
	socketPath string
	engine     Engine
	groupID    int
	allowedUID int
	mu         sync.Mutex
}

// NewServer creates a local runtime-helper server.
func NewServer(socketPath string, groupID int, allowedUID int, engine Engine) Server {
	return Server{socketPath: socketPath, groupID: groupID, allowedUID: allowedUID, engine: engine}
}

// Serve runs until the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("runtime helper server must run as root")
	}
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o750); err != nil {
		return err
	}
	if err := removeStaleSocket(s.socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(s.socketPath, 0o660); err != nil {
		return err
	}
	if s.groupID >= 0 {
		if err := os.Chown(s.socketPath, 0, s.groupID); err != nil {
			return err
		}
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, connection)
	}
}

func (s *Server) handle(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	peer, err := peerUID(connection)
	if err != nil || (peer >= 0 && peer != 0 && peer != s.allowedUID) {
		_ = json.NewEncoder(connection).Encode(errorResponse("FORBIDDEN_PEER", "Runtime helper peer is not authorized."))
		return
	}
	reader := bufio.NewReader(io.LimitReader(connection, maxHelperRequestBytes+1))
	data, err := reader.ReadBytes('\n')
	if err != nil || len(data) > maxHelperRequestBytes {
		_ = json.NewEncoder(connection).Encode(errorResponse("INVALID_REQUEST", "Runtime helper request is invalid."))
		return
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("INVALID_REQUEST", "Runtime helper request is invalid."))
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	encoder := json.NewEncoder(connection)
	var request Request
	if err := decoder.Decode(&request); err != nil {
		_ = encoder.Encode(errorResponse("INVALID_REQUEST", "Runtime helper request is invalid."))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		_ = encoder.Encode(errorResponse("INVALID_REQUEST", "Runtime helper request has trailing data."))
		return
	}
	if request.Operation == "dial_session_loopback" {
		s.handleSessionLoopback(ctx, connection, request)
		return
	}
	s.mu.Lock()
	result, err := s.engine.Execute(ctx, request)
	s.mu.Unlock()
	if err != nil {
		_ = encoder.Encode(errorResponse(classifyError(err), publicError(err)))
		return
	}
	_ = encoder.Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
}

func rejectDuplicateJSONKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("JSON value has trailing data")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is invalid")
			}
			if _, exists := seen[key]; exists {
				return errors.New("JSON object contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is incomplete")
		}
	default:
		return errors.New("JSON delimiter is invalid")
	}
	return nil
}

func (s *Server) handleSessionLoopback(ctx context.Context, connection net.Conn, request Request) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = json.NewEncoder(connection).Encode(errorResponse("INVALID_REQUEST", "Runtime helper requires a Unix connection."))
		return
	}
	forwardedConnection, err := s.engine.DialSessionLoopback(ctx, request)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse(classifyError(err), publicError(err)))
		return
	}
	defer forwardedConnection.Close()
	file, err := forwardedConnection.File()
	if err != nil {
		_ = json.NewEncoder(connection).Encode(errorResponse("RUNTIME_FAILED", "Runtime connection could not be transferred."))
		return
	}
	defer file.Close()
	data, err := json.Marshal(Response{Version: ProtocolVersion, OK: true, Result: map[string]any{"connected": true}})
	if err != nil {
		return
	}
	data = append(data, '\n')
	written, _, err := unixConnection.WriteMsgUnix(data, syscall.UnixRights(int(file.Fd())), nil)
	if err != nil || written != len(data) {
		return
	}
	ackDeadline := time.Now().Add(fileDescriptorAckTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(ackDeadline) {
		ackDeadline = contextDeadline
	}
	_ = connection.SetReadDeadline(ackDeadline)
	var acknowledgement [1]byte
	if _, err := io.ReadFull(connection, acknowledgement[:]); err != nil || acknowledgement[0] != fileDescriptorTransferAck {
		return
	}
}

func errorResponse(code string, message string) Response {
	return Response{Version: ProtocolVersion, OK: false, Error: &Error{Code: code, Message: message}}
}

func classifyError(err error) string {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "required") || strings.Contains(message, "invalid") || strings.Contains(message, "unsafe") || strings.Contains(message, "unsupported") {
		return "INVALID_SPEC"
	}
	if strings.Contains(message, "unavailable") || strings.Contains(message, "not found") {
		return "CAPABILITY_UNAVAILABLE"
	}
	return "RUNTIME_FAILED"
}

func publicError(err error) string {
	message := err.Error()
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("runtime socket path exists and is not a socket: %s", path)
	}
	return os.Remove(path)
}
