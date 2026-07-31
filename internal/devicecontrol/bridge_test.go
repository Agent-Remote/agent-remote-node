package devicecontrol

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

type fakeRelayClient struct {
	registrations atomic.Int32
	remotePeer    chan net.Conn
}

// RegisterDeviceRelayMaterial returns waiting once before issuing bounded test material.
func (client *fakeRelayClient) RegisterDeviceRelayMaterial(_ context.Context, _ string, request api.DeviceRelayMaterialRequest) (api.DeviceRelayMaterialResponse, error) {
	var response api.DeviceRelayMaterialResponse
	response.Data.Role = "proxy"
	response.Data.Generation = request.Generation
	if client.registrations.Add(1) == 1 {
		response.Data.Status = "waiting"
		return response, nil
	}
	response.Data.Status = "ready"
	response.Data.RelayPath = stringPointer("/api/v1/device-sessions/123e4567-e89b-42d3-a456-426614174003/relay")
	response.Data.RelayTicket = stringPointer("one-time-ticket")
	response.Data.PeerSPKISHA256 = stringPointer(strings.Repeat("b", 64))
	response.Data.ExporterContext = stringPointer(strings.Repeat("c", 64))
	response.Data.ExpiresAt = stringPointer(time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339Nano))
	return response, nil
}

// OpenDeviceRelay returns one side of an in-memory opaque relay.
func (client *fakeRelayClient) OpenDeviceRelay(_ context.Context, _ string, _ string, _ string) (io.ReadWriteCloser, error) {
	local, remote := net.Pipe()
	client.remotePeer <- remote
	return local, nil
}

// TestBridgeManagerValidatesBindingAndForwardsOpaqueBytes verifies bound opaque relay forwarding.
func TestBridgeManagerValidatesBindingAndForwardsOpaqueBytes(t *testing.T) {
	client := &fakeRelayClient{remotePeer: make(chan net.Conn, 1)}
	manager := NewBridgeManager(client)
	activation := testActivation(time.Now().Add(time.Minute))
	socketPath := shortBridgeSocketPath(t)
	if err := manager.Start(context.Background(), activation, socketPath); err != nil {
		t.Fatal(err)
	}
	defer manager.StopAll()
	local, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if err := writeBridgeFrame(local, bridgeHello{
		ProtocolVersion: 1, UserID: activation.UserID, DeviceID: activation.DeviceID,
		ToolSessionID: activation.ToolSessionID, DeviceSessionID: activation.DeviceSessionID,
		NodeID: activation.NodeID, Platform: activation.Platform, Generation: activation.Generation,
		SPKISHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	var material bridgeMaterial
	if err := readBridgeFrame(local, &material); err != nil {
		t.Fatal(err)
	}
	if material.Generation != activation.Generation || material.PeerSPKISHA256 != strings.Repeat("b", 64) ||
		material.ExporterContext != strings.Repeat("c", 64) {
		t.Fatalf("unexpected bridge material: %#v", material)
	}
	remote := <-client.remotePeer
	defer remote.Close()
	if _, err := local.Write([]byte("opaque-tls-record")); err != nil {
		t.Fatal(err)
	}
	request := make([]byte, len("opaque-tls-record"))
	if _, err := io.ReadFull(remote, request); err != nil || string(request) != "opaque-tls-record" {
		t.Fatalf("unexpected forwarded request %q: %v", request, err)
	}
	if _, err := remote.Write([]byte("opaque-response")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("opaque-response"))
	if _, err := io.ReadFull(local, response); err != nil || string(response) != "opaque-response" {
		t.Fatalf("unexpected forwarded response %q: %v", response, err)
	}
}

// TestBridgeManagerRejectsBindingBeforeControlPlaneExchange verifies local binding checks run first.
func TestBridgeManagerRejectsBindingBeforeControlPlaneExchange(t *testing.T) {
	client := &fakeRelayClient{remotePeer: make(chan net.Conn, 1)}
	manager := NewBridgeManager(client)
	activation := testActivation(time.Now().Add(time.Minute))
	socketPath := shortBridgeSocketPath(t)
	if err := manager.Start(context.Background(), activation, socketPath); err != nil {
		t.Fatal(err)
	}
	defer manager.StopAll()
	local, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if err := writeBridgeFrame(local, bridgeHello{
		ProtocolVersion: 1, UserID: activation.UserID, DeviceID: activation.DeviceID,
		ToolSessionID: activation.ToolSessionID, DeviceSessionID: activation.DeviceSessionID,
		NodeID: "123e4567-e89b-42d3-a456-426614174099", Platform: activation.Platform,
		Generation: activation.Generation, SPKISHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	var material bridgeMaterial
	if err := readBridgeFrame(local, &material); err == nil {
		t.Fatal("expected mismatched bridge binding to close the connection")
	}
	if client.registrations.Load() != 0 {
		t.Fatal("mismatched bridge binding reached the control plane")
	}
}

func TestReadBridgeFrameRejectsDuplicateKeys(t *testing.T) {
	payload := []byte(`{"protocol_version":1,"generation":1,"generation":2,"peer_spki_sha256":"digest","exporter_context":"context"}`)
	var frame bytes.Buffer
	if err := binary.Write(&frame, binary.BigEndian, uint32(len(payload))); err != nil {
		t.Fatal(err)
	}
	if _, err := frame.Write(payload); err != nil {
		t.Fatal(err)
	}
	var material bridgeMaterial
	if err := readBridgeFrame(&frame, &material); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("expected duplicate bridge key rejection, got %v", err)
	}
}

func testActivation(expiresAt time.Time) ActivatePayload {
	return ActivatePayload{
		ProtocolVersion: 1,
		UserID:          "123e4567-e89b-42d3-a456-426614174000", DeviceID: "123e4567-e89b-42d3-a456-426614174002",
		ToolSessionID: "123e4567-e89b-42d3-a456-426614174001", DeviceSessionID: "123e4567-e89b-42d3-a456-426614174003",
		NodeID: "123e4567-e89b-42d3-a456-426614174004", Platform: "macos", Generation: 1,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano), RuntimeBackend: "native",
	}
}

func stringPointer(value string) *string {
	return &value
}

func shortBridgeSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "ar-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "bridge.sock")
}
