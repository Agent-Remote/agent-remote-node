package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	decoder := json.NewDecoder(bufio.NewReader(connection))
	encoder := json.NewEncoder(connection)
	var request Request
	if err := decoder.Decode(&request); err != nil {
		_ = encoder.Encode(errorResponse("INVALID_REQUEST", "Runtime helper request is invalid."))
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
