package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

func TestClientCall(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "runtime.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer connection.Close()
		var request Request
		if decodeErr := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); decodeErr != nil {
			done <- decodeErr
			return
		}
		done <- json.NewEncoder(connection).Encode(Response{
			Version: ProtocolVersion,
			OK:      true,
			Result:  map[string]any{"operation": request.Operation},
		})
	}()
	result, err := NewClient(socketPath).Call(context.Background(), "task-1", "start_session", map[string]any{"session_id": "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result["operation"] != "start_session" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
