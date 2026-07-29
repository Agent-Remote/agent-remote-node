package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

const helperRoundTripTimeout = 10 * time.Second

func TestServerRejectsOversizedHelperRequest(t *testing.T) {
	payload := append(bytes.Repeat([]byte("x"), maxHelperRequestBytes+1), '\n')
	response := roundTripHelperRequest(t, payload)
	if response.OK || response.Error == nil || response.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("unexpected oversized request response: %#v", response)
	}
}

func TestServerRejectsUnknownAndTrailingRequestFields(t *testing.T) {
	for name, payload := range map[string][]byte{
		"unknown":          []byte(`{"version":1,"request_id":"request-1","operation":"probe","payload":{},"target":"host"}` + "\n"),
		"trailing":         []byte(`{"version":1,"request_id":"request-1","operation":"probe","payload":{}} {}` + "\n"),
		"duplicate nested": []byte(`{"version":1,"request_id":"request-1","operation":"dial_session_loopback","payload":{"session_id":"session-1","session_id":"session-2","runtime_backend":"native","port":5173}}` + "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			response := roundTripHelperRequest(t, payload)
			if response.OK || response.Error == nil || response.Error.Code != "INVALID_REQUEST" {
				t.Fatalf("unexpected invalid request response: %#v", response)
			}
		})
	}
}

func TestServerExecutesValidProbeRequest(t *testing.T) {
	response := roundTripHelperRequest(t, []byte(`{"version":1,"request_id":"request-1","operation":"probe","payload":{}}`+"\n"))
	if !response.OK || response.Error != nil || response.Version != ProtocolVersion {
		t.Fatalf("unexpected probe response: %#v", response)
	}
	if response.Result == nil {
		t.Fatal("probe response omitted result")
	}
}

func TestDuplicateKeyValidatorRejectsMalformedStructures(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":             {},
		"trailing object":   []byte(`{} {}`),
		"incomplete object": []byte(`{"key":`),
		"incomplete array":  []byte(`[{"key":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := rejectDuplicateJSONKeys(payload); err == nil {
				t.Fatal("malformed JSON was accepted")
			}
		})
	}
}

func roundTripHelperRequest(t *testing.T, payload []byte) Response {
	t.Helper()
	temporary, err := os.CreateTemp("", "agent-remote-runtime-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := NewServer(socketPath, -1, os.Getuid(), NewEngine(EngineConfig{StateRoot: t.TempDir()}))
	serverDone := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			server.handle(context.Background(), connection)
		}
		close(serverDone)
	}()
	connection, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(helperRoundTripTimeout)); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan struct{})
	go func() {
		_, _ = connection.Write(payload)
		close(writeDone)
	}()
	var response Response
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&response); err != nil {
		t.Fatal(err)
	}
	select {
	case <-serverDone:
	case <-time.After(helperRoundTripTimeout):
		t.Fatal("runtime helper handler did not stop")
	}
	select {
	case <-writeDone:
	case <-time.After(helperRoundTripTimeout):
		t.Fatal("runtime helper request writer did not stop")
	}
	return response
}
