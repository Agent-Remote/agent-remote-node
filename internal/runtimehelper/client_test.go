package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
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

func TestClientDialSessionLoopbackReceivesConnectedFileDescriptor(t *testing.T) {
	socketFile, err := os.CreateTemp("", "arpf-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := socketFile.Name()
	if err := socketFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
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
		forwarded, dialErr := net.Dial("tcp", upstream.Addr().String())
		if dialErr != nil {
			done <- dialErr
			return
		}
		defer forwarded.Close()
		application, acceptErr := upstream.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer application.Close()
		tcpConnection := forwarded.(*net.TCPConn)
		file, fileErr := tcpConnection.File()
		if fileErr != nil {
			done <- fileErr
			return
		}
		defer file.Close()
		response, marshalErr := json.Marshal(Response{Version: ProtocolVersion, OK: true})
		if marshalErr != nil {
			done <- marshalErr
			return
		}
		unixConnection := connection.(*net.UnixConn)
		_, _, writeErr := unixConnection.WriteMsgUnix(response, syscall.UnixRights(int(file.Fd())), nil)
		if writeErr != nil {
			done <- writeErr
			return
		}
		buffer := make([]byte, 4)
		if _, readErr := application.Read(buffer); readErr != nil {
			done <- readErr
			return
		}
		if string(buffer) != "ping" {
			done <- errors.New("unexpected forwarded payload")
			return
		}
		done <- nil
	}()

	clientConnection, err := NewClient(socketPath).DialSessionLoopback(
		context.Background(),
		"request-1",
		DialSessionLoopbackPayload{SessionID: "session-1", RuntimeBackend: "native", Port: 5173},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clientConnection.Close()
	if _, err := clientConnection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestParseFileDescriptorsParsesUnixRights(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rights := syscall.UnixRights(int(file.Fd()))
	descriptors, err := parseFileDescriptors(rights)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("unexpected descriptor count %d", len(descriptors))
	}
	closeFileDescriptors(descriptors)
}

func TestDialSessionLoopbackRejectsInvalidRequestsAndHelperResponses(t *testing.T) {
	payload := DialSessionLoopbackPayload{SessionID: "session-1", RuntimeBackend: "native", Port: 5173}
	if _, err := NewClient("unused").DialSessionLoopback(context.Background(), "", payload); err == nil {
		t.Fatal("empty request ID was accepted")
	}
	if _, err := NewClient(filepath.Join(t.TempDir(), "missing.sock")).DialSessionLoopback(context.Background(), "request-1", payload); err == nil {
		t.Fatal("missing helper socket was accepted")
	}

	responses := map[string][]byte{
		"malformed": []byte("not-json"),
		"wrong version": mustJSON(t, Response{
			Version: ProtocolVersion + 1, OK: true,
		}),
		"unspecified error": mustJSON(t, Response{
			Version: ProtocolVersion, OK: false,
		}),
		"explicit error": mustJSON(t, Response{
			Version: ProtocolVersion, OK: false,
			Error: &Error{Code: "INVALID_SPEC", Message: "rejected"},
		}),
		"missing descriptor": mustJSON(t, Response{
			Version: ProtocolVersion, OK: true,
		}),
	}
	for name, response := range responses {
		t.Run(name, func(t *testing.T) {
			socketPath, done := serveDialResponse(t, response)
			_, err := NewClient(socketPath).DialSessionLoopback(context.Background(), "request-1", payload)
			if err == nil {
				t.Fatal("invalid helper response was accepted")
			}
			if serverErr := <-done; serverErr != nil {
				t.Fatal(serverErr)
			}
		})
	}
}

func TestMapRejectsUnsupportedValues(t *testing.T) {
	if _, err := Map(make(chan int)); err == nil {
		t.Fatal("non-JSON payload was accepted")
	}
}

func serveDialResponse(t *testing.T, response []byte) (string, <-chan error) {
	t.Helper()
	temporary, err := os.CreateTemp("", "arpf-response-*.sock")
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
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer listener.Close()
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
		unixConnection := connection.(*net.UnixConn)
		_, _, writeErr := unixConnection.WriteMsgUnix(response, nil, nil)
		done <- writeErr
	}()
	return socketPath, done
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
