package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

const ProtocolVersion = 1

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
	var response Response
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode runtime helper response: %w", err)
	}
	if response.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported runtime helper response version %d", response.Version)
	}
	if !response.OK {
		if response.Error == nil {
			return nil, errors.New("runtime helper returned an unspecified error")
		}
		return nil, fmt.Errorf("runtime helper %s: %s", response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}

// Map converts a typed payload to a generic protocol payload.
func Map(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
