package devicecontrol

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

const (
	bridgeProtocolVersion = 1
	maximumBridgeFrame    = 4096
	relayMaterialRetry    = 100 * time.Millisecond
)

var hexDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type relayClient interface {
	RegisterDeviceRelayMaterial(context.Context, string, api.DeviceRelayMaterialRequest) (api.DeviceRelayMaterialResponse, error)
	OpenDeviceRelay(context.Context, string, string, string) (io.ReadWriteCloser, error)
}

type bridgeHello struct {
	ProtocolVersion int    `json:"protocol_version"`
	UserID          string `json:"user_id"`
	DeviceID        string `json:"device_id"`
	ToolSessionID   string `json:"tool_session_id"`
	DeviceSessionID string `json:"device_session_id"`
	NodeID          string `json:"node_id"`
	Platform        string `json:"platform"`
	Generation      uint64 `json:"generation"`
	SPKISHA256      string `json:"spki_sha256"`
}

type bridgeMaterial struct {
	ProtocolVersion int    `json:"protocol_version"`
	Generation      uint64 `json:"generation"`
	PeerSPKISHA256  string `json:"peer_spki_sha256"`
	ExporterContext string `json:"exporter_context"`
}

type bridgeSession struct {
	activation  ActivatePayload
	listener    net.Listener
	cancel      context.CancelFunc
	done        chan struct{}
	mu          sync.Mutex
	connections map[net.Conn]struct{}
}

type bridgeBindingKey struct {
	deviceSessionID string
	generation      uint64
}

// BridgeManager owns generation-bound local listeners and opaque relay connections.
type BridgeManager struct {
	client   relayClient
	mu       sync.Mutex
	sessions map[bridgeBindingKey]*bridgeSession
}

// NewBridgeManager creates a local device relay manager backed by the authenticated node client.
func NewBridgeManager(client relayClient) *BridgeManager {
	return &BridgeManager{client: client, sessions: make(map[bridgeBindingKey]*bridgeSession)}
}

// Start replaces an older tool-session listener with one exact activation generation.
func (m *BridgeManager) Start(parent context.Context, activation ActivatePayload, socketPath string) error {
	if err := validateBridgeSocketPath(socketPath); err != nil {
		return err
	}
	if err := m.stopConflictingActivations(activation); err != nil {
		return err
	}
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("device bridge path is occupied by a non-socket entry")
		}
		if err := os.Remove(socketPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0o666); err != nil {
		listener.Close()
		os.Remove(socketPath)
		return err
	}
	deadline, err := time.Parse(time.RFC3339Nano, activation.ExpiresAt)
	if err != nil || !deadline.After(time.Now()) {
		listener.Close()
		os.Remove(socketPath)
		return errors.New("device bridge activation expiry is invalid")
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	session := &bridgeSession{
		activation: activation, listener: listener, cancel: cancel, done: make(chan struct{}),
		connections: make(map[net.Conn]struct{}),
	}
	m.mu.Lock()
	m.sessions[bridgeBindingKey{
		deviceSessionID: activation.DeviceSessionID,
		generation:      activation.Generation,
	}] = session
	m.mu.Unlock()
	go m.serve(ctx, socketPath, session)
	return nil
}

func (m *BridgeManager) stopConflictingActivations(activation ActivatePayload) error {
	m.mu.Lock()
	keys := make([]bridgeBindingKey, 0)
	for key, session := range m.sessions {
		current := session.activation
		if current.DeviceSessionID != activation.DeviceSessionID && current.ToolSessionID != activation.ToolSessionID {
			continue
		}
		if current.DeviceSessionID == activation.DeviceSessionID && current.Generation > activation.Generation {
			m.mu.Unlock()
			return errors.New("device bridge activation generation is stale")
		}
		keys = append(keys, key)
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.Stop(key.deviceSessionID, key.generation)
	}
	return nil
}

// Stop closes the listener and active relay streams for one tool session.
func (m *BridgeManager) Stop(deviceSessionID string, generation uint64) {
	key := bridgeBindingKey{deviceSessionID: deviceSessionID, generation: generation}
	m.mu.Lock()
	session := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if session == nil {
		return
	}
	session.cancel()
	_ = session.listener.Close()
	session.mu.Lock()
	for connection := range session.connections {
		_ = connection.Close()
	}
	session.mu.Unlock()
	<-session.done
}

// StopThrough closes generations up to one exact device-session revocation boundary.
func (m *BridgeManager) StopThrough(deviceSessionID string, generation uint64) {
	m.mu.Lock()
	keys := make([]bridgeBindingKey, 0)
	for key := range m.sessions {
		if key.deviceSessionID == deviceSessionID && key.generation <= generation {
			keys = append(keys, key)
		}
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.Stop(key.deviceSessionID, key.generation)
	}
}

// StopToolSession closes all generations belonging to one Claude session.
func (m *BridgeManager) StopToolSession(toolSessionID string) {
	m.mu.Lock()
	keys := make([]bridgeBindingKey, 0)
	for key, session := range m.sessions {
		if session.activation.ToolSessionID == toolSessionID {
			keys = append(keys, key)
		}
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.Stop(key.deviceSessionID, key.generation)
	}
}

// StopAll closes every listener and active relay stream owned by the manager.
func (m *BridgeManager) StopAll() {
	m.mu.Lock()
	keys := make([]bridgeBindingKey, 0, len(m.sessions))
	for key := range m.sessions {
		keys = append(keys, key)
	}
	m.mu.Unlock()
	for _, key := range keys {
		m.Stop(key.deviceSessionID, key.generation)
	}
}

func (m *BridgeManager) serve(ctx context.Context, socketPath string, session *bridgeSession) {
	defer close(session.done)
	defer os.Remove(socketPath)
	defer session.cancel()
	go func() {
		<-ctx.Done()
		_ = session.listener.Close()
		session.closeConnections()
	}()
	for {
		connection, err := session.listener.Accept()
		if err != nil {
			return
		}
		session.mu.Lock()
		session.connections[connection] = struct{}{}
		session.mu.Unlock()
		go func() {
			defer func() {
				_ = connection.Close()
				session.mu.Lock()
				delete(session.connections, connection)
				session.mu.Unlock()
			}()
			_ = m.handle(ctx, session.activation, connection)
		}()
	}
}

func (session *bridgeSession) closeConnections() {
	session.mu.Lock()
	defer session.mu.Unlock()
	for connection := range session.connections {
		_ = connection.Close()
	}
}

func (m *BridgeManager) handle(ctx context.Context, activation ActivatePayload, local net.Conn) error {
	var hello bridgeHello
	if err := readBridgeFrame(local, &hello); err != nil {
		return err
	}
	if err := validateBridgeHello(hello, activation); err != nil {
		return err
	}
	material, err := m.awaitRelayMaterial(ctx, activation, hello.SPKISHA256)
	if err != nil {
		return err
	}
	remote, err := m.client.OpenDeviceRelay(ctx, activation.DeviceSessionID, material.relayPath, material.relayTicket)
	if err != nil {
		return err
	}
	defer remote.Close()
	if err := writeBridgeFrame(local, bridgeMaterial{
		ProtocolVersion: bridgeProtocolVersion, Generation: activation.Generation,
		PeerSPKISHA256: material.peerSPKI, ExporterContext: material.exporterContext,
	}); err != nil {
		return err
	}
	return copyOpaqueRelay(local, remote)
}

type readyRelayMaterial struct {
	relayPath       string
	relayTicket     string
	peerSPKI        string
	exporterContext string
}

func (m *BridgeManager) awaitRelayMaterial(ctx context.Context, activation ActivatePayload, spkiSHA256 string) (readyRelayMaterial, error) {
	for {
		response, err := m.client.RegisterDeviceRelayMaterial(ctx, activation.DeviceSessionID, api.DeviceRelayMaterialRequest{
			Generation: activation.Generation, SPKISHA256: spkiSHA256,
		})
		if err != nil {
			return readyRelayMaterial{}, err
		}
		if response.Data.Role != "proxy" || response.Data.Generation != activation.Generation {
			return readyRelayMaterial{}, errors.New("device relay material binding is invalid")
		}
		if response.Data.Status == "ready" {
			if response.Data.RelayPath == nil || response.Data.RelayTicket == nil ||
				response.Data.PeerSPKISHA256 == nil || response.Data.ExporterContext == nil ||
				response.Data.ExpiresAt == nil ||
				!hexDigestPattern.MatchString(*response.Data.PeerSPKISHA256) ||
				!hexDigestPattern.MatchString(*response.Data.ExporterContext) {
				return readyRelayMaterial{}, errors.New("ready device relay material is incomplete")
			}
			expiresAt, expiresErr := time.Parse(time.RFC3339Nano, *response.Data.ExpiresAt)
			activationExpiry, activationErr := time.Parse(time.RFC3339Nano, activation.ExpiresAt)
			if expiresErr != nil || activationErr != nil || !expiresAt.After(time.Now()) || expiresAt.After(activationExpiry) {
				return readyRelayMaterial{}, errors.New("device relay material expiry is invalid")
			}
			return readyRelayMaterial{
				relayPath: *response.Data.RelayPath, relayTicket: *response.Data.RelayTicket,
				peerSPKI: *response.Data.PeerSPKISHA256, exporterContext: *response.Data.ExporterContext,
			}, nil
		}
		if response.Data.Status != "waiting" {
			return readyRelayMaterial{}, errors.New("device relay material status is invalid")
		}
		timer := time.NewTimer(relayMaterialRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return readyRelayMaterial{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func validateBridgeSocketPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Base(path) != "bridge.sock" {
		return errors.New("device bridge socket path is invalid")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("device bridge socket directory is unsafe")
	}
	return nil
}

func validateBridgeHello(hello bridgeHello, activation ActivatePayload) error {
	if hello.ProtocolVersion != bridgeProtocolVersion || hello.UserID != activation.UserID ||
		hello.DeviceID != activation.DeviceID || hello.ToolSessionID != activation.ToolSessionID ||
		hello.DeviceSessionID != activation.DeviceSessionID || hello.NodeID != activation.NodeID ||
		hello.Platform != activation.Platform || hello.Generation != activation.Generation ||
		!hexDigestPattern.MatchString(hello.SPKISHA256) {
		return errors.New("device bridge hello does not match activation")
	}
	return nil
}

func readBridgeFrame(reader io.Reader, destination any) error {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return err
	}
	if length == 0 || length > maximumBridgeFrame {
		return errors.New("device bridge control frame is invalid")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	return decodeStrictJSONObject(data, destination, "device bridge frame")
}

func decodeStrictJSONObject(data []byte, destination any, subject string) error {
	if err := rejectDuplicateJSONObjectKeys(data, subject); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s must contain one JSON object", subject)
	}
	return nil
}

func rejectDuplicateJSONObjectKeys(data []byte, subject string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return fmt.Errorf("%s must contain one JSON object", subject)
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("%s contains an invalid key", subject)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s contains a duplicate key", subject)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return fmt.Errorf("%s must contain one JSON object", subject)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s must contain one JSON object", subject)
	}
	return nil
}

func writeBridgeFrame(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maximumBridgeFrame {
		return errors.New("device bridge control frame is invalid")
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func copyOpaqueRelay(local io.ReadWriteCloser, remote io.ReadWriteCloser) error {
	errorsChannel := make(chan error, 2)
	go func() {
		_, err := io.Copy(remote, local)
		errorsChannel <- err
	}()
	go func() {
		_, err := io.Copy(local, remote)
		errorsChannel <- err
	}()
	first := <-errorsChannel
	_ = local.Close()
	_ = remote.Close()
	second := <-errorsChannel
	return errors.Join(first, second)
}
