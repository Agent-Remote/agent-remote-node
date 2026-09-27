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

func TestServerProbeDoesNotWaitForLifecycleMutation(t *testing.T) {
	response := roundTripHelperRequestWithSetup(t, []byte(`{"version":1,"request_id":"probe-during-copy","operation":"probe","payload":{}}`+"\n"), func(server *Server) {
		server.mu.Lock()
		t.Cleanup(server.mu.Unlock)
	})
	if !response.OK || response.Result == nil {
		t.Fatal("read-only capability probe was blocked by an independent lifecycle mutation")
	}
}

func TestServerPreservesTerminalDeviceGeneration(t *testing.T) {
	response := roundTripHelperRequest(t, []byte(`{"version":1,"request_id":"request-1","operation":"clear_device_control_context","payload":{"device_session_id":"223e4567-e89b-42d3-a456-426614174001","tool_session_id":"323e4567-e89b-42d3-a456-426614174002","generation":9223372036854775807,"inclusive":true}}`+"\n"))
	if !response.OK || response.Error != nil {
		t.Fatalf("terminal generation lost precision: %#v", response)
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
	return roundTripHelperRequestWithSetup(t, payload, nil)
}

func roundTripHelperRequestWithSetup(t *testing.T, payload []byte, setup func(*Server)) Response {
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
	if setup != nil {
		setup(&server)
	}
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

func TestHelperLargeFramesAreRestrictedToConfigurationImport(t *testing.T) {
	for _, operation := range []string{"probe", "import_account_config"} {
		t.Run(operation, func(t *testing.T) {
			body, err := json.Marshal(Request{Version: 1, RequestID: "request-1", Operation: operation, Payload: map[string]any{"padding": string(bytes.Repeat([]byte("x"), maxHelperRequestBytes))}})
			if err != nil {
				t.Fatal(err)
			}
			response := roundTripHelperRequest(t, append(body, '\n'))
			if response.OK || response.Error == nil {
				t.Fatalf("invalid payload accepted: %#v", response)
			}
			if operation == "probe" && response.Error.Code != "INVALID_REQUEST" {
				t.Fatal("ordinary request limit was widened")
			}
			if operation == "import_account_config" && response.Error.Code == "INVALID_REQUEST" {
				t.Fatal("import frame retained the obsolete one MiB limit")
			}
		})
	}
}
