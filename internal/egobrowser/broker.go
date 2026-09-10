// Package egobrowser contains the Node-local broker for the ego-browser bridge.
//
// The broker is deliberately the only Node component that can exchange a
// browser binding for relay material.  Wrapper processes receive short-lived
// permits over an owner-only Unix socket; scripts, tickets, and relay keys are
// never written to disk or included in diagnostics.
package egobrowser

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/net/websocket"
)

const (
	// ProtocolVersion is the browser bridge protocol accepted by the broker.
	ProtocolVersion = "ego-browser-bridge-v1"
	// InnerProtocolVersion is the encrypted request protocol advertised to wrappers.
	InnerProtocolVersion                = "ego-browser-bridge-v1-inner"
	defaultMaxScriptBytes               = 1 << 20
	defaultMaxExecuteTimeoutMS          = 120_000
	defaultMaxPayloadBytes              = 16 * 1024 * 1024
	defaultLeaseSeconds                 = 60
	defaultRenewIntervalSeconds         = 20
	defaultRenewGraceSeconds            = 10
	defaultAdmissionMinRemainingSeconds = 20
	keyWrapBytes                        = 92
	maximumAbandonedRelayRequests       = 4096
)

var requiredBridgeCapabilities = [...]string{
	"ego_browser_script_execute_v1",
	"ego_browser_snapshot_v1",
	"ego_browser_screenshot_artifact_v1",
	"ego_browser_task_space_v1",
	"ego_browser_concurrency_v1",
}

var binaryRelayMessage = websocket.Codec{
	Marshal: func(value any) ([]byte, byte, error) {
		data, ok := value.([]byte)
		if !ok {
			return nil, websocket.UnknownFrame, websocket.ErrNotSupported
		}
		return data, websocket.BinaryFrame, nil
	},
	Unmarshal: func(data []byte, payloadType byte, value any) error {
		if payloadType != websocket.BinaryFrame {
			return fmt.Errorf("%w: relay message must be binary", ErrProtocol)
		}
		destination, ok := value.(*[]byte)
		if !ok {
			return websocket.ErrNotSupported
		}
		*destination = data
		return nil
	},
}

var (
	// ErrDisabled indicates that the Node has no enabled browser bridge.
	ErrDisabled = errors.New("ego-browser bridge is disabled")
	// ErrUnavailable indicates that no current authorized binding is available.
	ErrUnavailable = errors.New("ego-browser bridge is unavailable")
	// ErrLeaseRenewalRequired indicates that a permit needs a successful renewal first.
	ErrLeaseRenewalRequired = errors.New("ego-browser binding lease renewal required")
	// ErrLeaseExpired indicates that the binding lease or absolute TTL has ended.
	ErrLeaseExpired = errors.New("ego-browser binding lease expired")
	// ErrRevoked indicates that the binding generation was revoked.
	ErrRevoked = errors.New("ego-browser binding revoked")
	// ErrReplay indicates that a permit or sequence was already consumed.
	ErrReplay = errors.New("ego-browser request permit was already consumed")
	// ErrConcurrency indicates that the binding's parallel request limit is full.
	ErrConcurrency = errors.New("ego-browser binding concurrency limit reached")
	// ErrProtocol indicates malformed broker IPC or relay metadata.
	ErrProtocol = errors.New("ego-browser broker protocol error")
)

// Config controls the broker's socket, persistence, and admission limits.
type Config struct {
	Enabled                      bool
	NodeID                       string
	SocketPath                   string
	StateRoot                    string
	Client                       api.Client
	ControlPlaneConfigured       bool
	LeaseSeconds                 int
	RenewIntervalSeconds         int
	RenewGraceSeconds            int
	AdmissionMinRemainingSeconds int
	MaxParallelRequests          int
	MaxScriptBytes               int
	MaxExecuteTimeoutMS          int
	WrapperVersion               string
	// GrantPeerAccess gives one trusted runtime UID access to the broker socket.
	// Production uses a narrow POSIX ACL; tests may inject an equivalent hook.
	GrantPeerAccess func(socketPath string, uid uint32) error
}

func (c Config) withDefaults() Config {
	if c.SocketPath == "" {
		c.SocketPath = "/run/agent-remote-node/ego-browser-broker.sock"
	}
	if c.StateRoot == "" {
		c.StateRoot = "/var/lib/agent-remote-node/ego-browser"
	}
	if c.LeaseSeconds <= 0 {
		c.LeaseSeconds = defaultLeaseSeconds
	}
	if c.RenewIntervalSeconds <= 0 {
		c.RenewIntervalSeconds = defaultRenewIntervalSeconds
	}
	if c.RenewGraceSeconds <= 0 {
		c.RenewGraceSeconds = defaultRenewGraceSeconds
	}
	if c.AdmissionMinRemainingSeconds <= 0 {
		c.AdmissionMinRemainingSeconds = defaultAdmissionMinRemainingSeconds
	}
	if c.MaxParallelRequests <= 0 {
		c.MaxParallelRequests = 4
	}
	if c.MaxScriptBytes <= 0 {
		c.MaxScriptBytes = defaultMaxScriptBytes
	}
	if c.MaxExecuteTimeoutMS <= 0 {
		c.MaxExecuteTimeoutMS = defaultMaxExecuteTimeoutMS
	}
	if c.WrapperVersion == "" {
		c.WrapperVersion = egobrowserartifact.PinnedWrapperVersion
	}
	if c.GrantPeerAccess == nil {
		c.GrantPeerAccess = grantPeerSocketAccess
	}
	return c
}

// PermitRequest is the untrusted, metadata-only request sent by a wrapper.
type PermitRequest struct {
	Protocol        string `json:"protocol"`
	Type            string `json:"type"`
	StartupNonce    string `json:"startup_nonce"`
	ScriptBytes     int    `json:"script_bytes"`
	TimeoutMS       int    `json:"timeout_ms"`
	CWDLabel        string `json:"cwd_label"`
	ConcurrencyMode string `json:"concurrency_mode"`
	TaskSpaceScope  string `json:"task_space_scope,omitempty"`
	TabScope        string `json:"tab_scope,omitempty"`
}

// Permit is a one-time authorization bound to a binding generation.
type Permit struct {
	BindingID            string
	Generation           uint64
	RequestID            string
	Sequence             uint64
	ExpiresAt            time.Time
	MaxPayloadBytes      int
	MaxScriptBytes       int
	DefaultTaskSpace     string
	AllowlistRevision    uint64
	LearningBundleDigest *string
	ConcurrencyMode      string
	TaskSpaceScope       string
	TabScope             string
	SessionKey           [32]byte
}

// Broker owns browser binding state and the wrapper IPC endpoint.
type Broker struct {
	cfg Config

	mu       sync.RWMutex
	bindings map[string]*bindingState
	sessions map[string]string
	nonces   map[string]string
	peerUIDs map[string]uint32
	permits  map[uint64]*Permit
	consumed map[uint64]time.Time
	active   map[uint64]*activeRequest
	sequence uint64
	changed  chan struct{}

	listenerMu sync.Mutex
	listener   *net.UnixListener
	closeOnce  sync.Once
	wg         sync.WaitGroup
	closed     chan struct{}
}

type bindingState struct {
	metadata           api.EgoBrowserBinding
	relay              *relaySession
	relayMu            sync.Mutex
	relayEpoch         atomic.Uint64
	terminal           bool
	terminalGeneration uint64
}

type activeRequest struct {
	bindingID              string
	generation             uint64
	requestID              string
	sequence               uint64
	expiresAt              time.Time
	submitted              bool
	sent                   bool
	serverTerminalObserved bool
	cancel                 chan struct{}
	cancelOnce             sync.Once
	forwardDone            chan struct{}
	forwardDoneOnce        sync.Once
}

type relayRequestKey struct {
	bindingID  string
	generation uint64
	requestID  string
	sequence   uint64
}

type relayResult struct {
	frame []byte
	err   error
}

type pendingRelayRequest struct {
	request outerEnvelope
	result  chan relayResult
}

type relaySession struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	pending    map[relayRequestKey]pendingRelayRequest
	abandoned  map[relayRequestKey]struct{}
	closed     atomic.Bool
	closeOnce  sync.Once
}

type sequenceState struct {
	Version      int    `json:"version"`
	NextSequence uint64 `json:"next_sequence"`
}

// New creates a broker and loads its owner-only monotonic sequence state.
func New(config Config) (*Broker, error) {
	config = config.withDefaults()
	if config.MaxParallelRequests < 1 || config.MaxParallelRequests > 4 ||
		config.MaxScriptBytes < 1 || config.MaxScriptBytes > defaultMaxScriptBytes ||
		config.MaxExecuteTimeoutMS < 1 || config.MaxExecuteTimeoutMS > defaultMaxExecuteTimeoutMS ||
		config.RenewIntervalSeconds >= config.LeaseSeconds {
		return nil, fmt.Errorf("%w: broker limits", ErrProtocol)
	}
	if config.StateRoot != "" {
		if err := preparePrivateRoot(config.StateRoot); err != nil {
			return nil, err
		}
	}
	sequence, err := loadSequence(config.StateRoot)
	if err != nil {
		return nil, err
	}
	return &Broker{
		cfg:      config,
		bindings: make(map[string]*bindingState),
		sessions: make(map[string]string),
		nonces:   make(map[string]string),
		peerUIDs: make(map[string]uint32),
		permits:  make(map[uint64]*Permit),
		consumed: make(map[uint64]time.Time),
		active:   make(map[uint64]*activeRequest),
		sequence: sequence,
		changed:  make(chan struct{}),
		closed:   make(chan struct{}),
	}, nil
}

// SocketPath returns the owner-only IPC path configured for this broker.
func (b *Broker) SocketPath() string { return b.cfg.SocketPath }

// PrepareSocket creates the broker endpoint before a sandbox mounts its parent
// directory. Peer authorization still occurs separately for each session.
func (b *Broker) PrepareSocket() error {
	_, err := b.listen()
	return err
}

// RegisterToolSession returns the process-local capability for one tool
// session. Repeated registration is idempotent until the session is removed;
// broker restart intentionally loses every capability.
func (b *Broker) RegisterToolSession(toolSessionID string) (string, bool, error) {
	if !b.cfg.Enabled {
		return "", false, ErrDisabled
	}
	if !validOpaqueText(toolSessionID, 128) {
		return "", false, fmt.Errorf("%w: tool session identity", ErrProtocol)
	}
	if _, err := dedicatedTaskSpace(toolSessionID); err != nil {
		return "", false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.closed:
		return "", false, ErrUnavailable
	default:
	}
	if nonce := b.sessions[toolSessionID]; nonce != "" {
		return nonce, false, nil
	}
	for {
		nonce, err := randomToken(32)
		if err != nil {
			return "", false, err
		}
		if _, exists := b.nonces[nonce]; exists {
			continue
		}
		b.sessions[toolSessionID] = nonce
		b.nonces[nonce] = toolSessionID
		return nonce, true, nil
	}
}

// AuthorizeToolSessionPeer binds a session capability to the exact runtime UID
// reported by the trusted runtime helper and grants that UID socket access.
func (b *Broker) AuthorizeToolSessionPeer(toolSessionID, expectedNonce string, uid uint32) error {
	if !b.cfg.Enabled {
		return ErrDisabled
	}
	if !validOpaqueText(toolSessionID, 128) || !validOpaqueText(expectedNonce, 256) {
		return fmt.Errorf("%w: tool session peer identity", ErrProtocol)
	}
	b.mu.RLock()
	registeredNonce := b.sessions[toolSessionID]
	_, alreadyAuthorized := b.peerUIDs[toolSessionID]
	b.mu.RUnlock()
	if registeredNonce != expectedNonce {
		return ErrUnavailable
	}
	if alreadyAuthorized {
		b.mu.RLock()
		existingUID := b.peerUIDs[toolSessionID]
		b.mu.RUnlock()
		if existingUID != uid {
			return fmt.Errorf("%w: tool session peer uid changed", ErrProtocol)
		}
		return nil
	}
	if _, err := b.listen(); err != nil {
		return fmt.Errorf("prepare ego-browser broker socket: %w", err)
	}
	if err := b.cfg.GrantPeerAccess(b.cfg.SocketPath, uid); err != nil {
		return fmt.Errorf("grant ego-browser broker peer access: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessions[toolSessionID] != expectedNonce || b.nonces[expectedNonce] != toolSessionID {
		return ErrUnavailable
	}
	if existingUID, exists := b.peerUIDs[toolSessionID]; exists && existingUID != uid {
		return fmt.Errorf("%w: tool session peer uid changed", ErrProtocol)
	}
	b.peerUIDs[toolSessionID] = uid
	return nil
}

// UnregisterToolSession removes one process-local session capability. When
// expectedNonce is non-empty, removal occurs only if it still names that exact
// capability, which permits safe rollback of a failed session start.
func (b *Broker) UnregisterToolSession(toolSessionID, expectedNonce string) bool {
	if toolSessionID == "" {
		return false
	}
	var toClose []*bindingState
	b.mu.Lock()
	nonce := b.sessions[toolSessionID]
	if nonce == "" || expectedNonce != "" && nonce != expectedNonce {
		b.mu.Unlock()
		return false
	}
	delete(b.sessions, toolSessionID)
	delete(b.nonces, nonce)
	delete(b.peerUIDs, toolSessionID)
	for bindingID, state := range b.bindings {
		if state.metadata.ToolSessionID == toolSessionID {
			b.clearBindingMaterialsLocked(bindingID)
			toClose = append(toClose, state)
		}
	}
	b.mu.Unlock()
	for _, state := range toClose {
		closeRelay(state, ErrRevoked)
	}
	return true
}

// SetBindings replaces the in-memory binding snapshot from the control plane.
func (b *Broker) SetBindings(bindings []api.EgoBrowserBinding) error {
	if !b.cfg.Enabled {
		return ErrDisabled
	}
	next := make(map[string]*bindingState, len(bindings))
	for _, metadata := range bindings {
		if err := b.validateBinding(metadata); err != nil {
			return err
		}
		if _, exists := next[metadata.BindingID]; exists {
			return fmt.Errorf("%w: duplicate binding", ErrProtocol)
		}
		next[metadata.BindingID] = &bindingState{metadata: cloneBinding(metadata)}
	}
	var toClose []*bindingState
	b.mu.Lock()
	for id, old := range b.bindings {
		fresh := next[id]
		if fresh == nil {
			b.clearBindingMaterialsLocked(id)
			toClose = append(toClose, old)
			continue
		}
		// A delayed control-plane response must never move a binding backwards.
		if fresh.metadata.Generation < old.metadata.Generation {
			next[id] = old
			continue
		}
		// Local terminal state is monotonic within a generation.  A stale
		// snapshot claiming the same generation is not allowed to revive it.
		if old.terminal && fresh.metadata.Generation <= old.terminalGeneration {
			next[id] = old
			continue
		}
		if fresh.metadata.Generation != old.metadata.Generation {
			b.clearBindingMaterialsLocked(id)
			toClose = append(toClose, old)
			old.terminal = false
			old.terminalGeneration = 0
		}
		if old.metadata.ToolSessionID != fresh.metadata.ToolSessionID ||
			old.metadata.AllowlistRevision != fresh.metadata.AllowlistRevision ||
			!sameStringPointer(old.metadata.LearningBundleDigest, fresh.metadata.LearningBundleDigest) ||
			old.metadata.Status != fresh.metadata.Status || old.metadata.LeaseHealth != fresh.metadata.LeaseHealth {
			toClose = append(toClose, old)
			b.clearBindingMaterialsLocked(id)
		}
		// Keep the existing state object for an unchanged generation. Besides
		// preserving the live relay, this retains its synchronization primitive
		// without ever copying a sync.Mutex.
		old.metadata = fresh.metadata
		next[id] = old
	}
	b.bindings = next
	b.mu.Unlock()
	activeBindings := 0
	for _, state := range next {
		if state.metadata.Status == "active" && state.metadata.LeaseHealth == "healthy" {
			activeBindings++
		}
	}
	log.Printf("metric=ego_browser_bindings_active value=%d unit=bindings", activeBindings)
	for _, state := range toClose {
		closeRelay(state, ErrRevoked)
	}
	return nil
}

// Bindings returns a copy of the current binding metadata without secrets.
func (b *Broker) Bindings() []api.EgoBrowserBinding {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]api.EgoBrowserBinding, 0, len(b.bindings))
	for _, state := range b.bindings {
		result = append(result, cloneBinding(state.metadata))
	}
	return result
}

// Refresh fetches the current Node binding snapshot from the control plane.
func (b *Broker) Refresh(ctx context.Context) error {
	if !b.cfg.Enabled {
		return ErrDisabled
	}
	if !b.cfg.ControlPlaneConfigured {
		return ErrUnavailable
	}
	response, err := b.cfg.Client.ListEgoBrowserBindings(ctx)
	if err != nil {
		return err
	}
	for _, binding := range response.Data.Items {
		if b.cfg.NodeID != "" && binding.NodeID != b.cfg.NodeID {
			return fmt.Errorf("%w: binding node mismatch", ErrProtocol)
		}
	}
	return b.SetBindings(response.Data.Items)
}

// IssuePermit validates wrapper metadata and returns a one-time permit.
func (b *Broker) IssuePermit(ctx context.Context, request PermitRequest) (Permit, error) {
	if !b.cfg.Enabled {
		return Permit{}, ErrDisabled
	}
	if request.Protocol != ProtocolVersion || request.Type != "permit_request" {
		return Permit{}, fmt.Errorf("%w: permit request metadata", ErrProtocol)
	}
	if request.ScriptBytes <= 0 || request.ScriptBytes > b.cfg.MaxScriptBytes {
		return Permit{}, fmt.Errorf("%w: script size", ErrProtocol)
	}
	if request.TimeoutMS <= 0 || request.TimeoutMS > b.cfg.MaxExecuteTimeoutMS {
		return Permit{}, fmt.Errorf("%w: timeout", ErrProtocol)
	}
	if !validOpaqueText(request.CWDLabel, 128) || strings.Contains(request.CWDLabel, "..") {
		return Permit{}, fmt.Errorf("%w: cwd label", ErrProtocol)
	}
	toolSessionID, state, err := b.bindingForNonce(request.StartupNonce)
	if err != nil {
		return Permit{}, ErrUnavailable
	}
	defaultTaskSpace, err := dedicatedTaskSpace(toolSessionID)
	if err != nil {
		return Permit{}, err
	}
	scope, err := normalizeScope(request.ConcurrencyMode, request.TaskSpaceScope, request.TabScope)
	if err != nil {
		return Permit{}, err
	}
	if scope.mode != "binding" {
		if scope.taskSpace != defaultTaskSpace {
			return Permit{}, fmt.Errorf("%w: task space scope is not dedicated to the tool session", ErrProtocol)
		}
		// The authenticated nonce, rather than wrapper metadata, owns this value.
		scope.taskSpace = defaultTaskSpace
	}
	if err := b.ensureLease(ctx, state); err != nil {
		return Permit{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	b.pruneExpiredPermitsLocked(now)
	// Refresh or session stop may have replaced either side of the association
	// while renewal was in flight.
	if b.nonces[request.StartupNonce] != toolSessionID {
		return Permit{}, ErrUnavailable
	}
	state, err = b.bindingForSessionLocked(toolSessionID)
	if err != nil {
		return Permit{}, ErrUnavailable
	}
	metadata := state.metadata
	if metadata.Status != "active" || metadata.LeaseHealth != "healthy" {
		return Permit{}, ErrLeaseRenewalRequired
	}
	if metadata.Generation == 0 {
		return Permit{}, fmt.Errorf("%w: generation", ErrProtocol)
	}
	parallelLimit := minInt(b.cfg.MaxParallelRequests, metadata.MaxParallelRequests)
	if b.activeRequestCountLocked(metadata.BindingID) >= parallelLimit {
		return Permit{}, ErrConcurrency
	}
	sequence, err := b.nextSequenceLocked()
	if err != nil {
		return Permit{}, err
	}
	requestID, err := randomToken(18)
	if err != nil {
		return Permit{}, err
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return Permit{}, err
	}
	expiresAt := now.Add(20 * time.Second)
	if metadata.LeaseUntil != nil && *metadata.LeaseUntil != "" {
		leaseUntil, parseErr := time.Parse(time.RFC3339Nano, *metadata.LeaseUntil)
		if parseErr != nil {
			return Permit{}, fmt.Errorf("%w: lease timestamp", ErrProtocol)
		}
		if leaseUntil.Before(expiresAt) {
			expiresAt = leaseUntil
		}
	}
	if metadata.AbsoluteTTLUntil != "" {
		absoluteTTL, parseErr := time.Parse(time.RFC3339Nano, metadata.AbsoluteTTLUntil)
		if parseErr != nil {
			return Permit{}, fmt.Errorf("%w: absolute TTL timestamp", ErrProtocol)
		}
		if absoluteTTL.Before(expiresAt) {
			expiresAt = absoluteTTL
		}
	}
	if !expiresAt.After(now) {
		return Permit{}, ErrLeaseExpired
	}
	permit := Permit{
		BindingID:            metadata.BindingID,
		Generation:           metadata.Generation,
		RequestID:            requestID,
		Sequence:             sequence,
		ExpiresAt:            expiresAt,
		MaxPayloadBytes:      defaultMaxPayloadBytes,
		MaxScriptBytes:       b.cfg.MaxScriptBytes,
		DefaultTaskSpace:     defaultTaskSpace,
		AllowlistRevision:    metadata.AllowlistRevision,
		LearningBundleDigest: cloneStringPointer(metadata.LearningBundleDigest),
		ConcurrencyMode:      scope.mode,
		TaskSpaceScope:       scope.taskSpace,
		TabScope:             scope.tab,
		SessionKey:           key,
	}
	b.permits[sequence] = &permit
	b.active[sequence] = &activeRequest{
		bindingID:   metadata.BindingID,
		generation:  metadata.Generation,
		requestID:   requestID,
		sequence:    sequence,
		expiresAt:   expiresAt,
		cancel:      make(chan struct{}),
		forwardDone: make(chan struct{}),
	}
	b.notifyRequestChangeLocked()
	return permit, nil
}

// CancelRequest signals cancellation for one exact browser request. A missing
// request is an idempotent no-op because its terminal response may have won the
// race with a control-plane cancellation task.
func (b *Broker) CancelRequest(bindingID string, generation uint64, requestID string, sequence uint64) (bool, error) {
	if !validOpaqueText(bindingID, 128) || generation == 0 || !validOpaqueText(requestID, 128) || sequence == 0 {
		return false, fmt.Errorf("%w: cancellation identity", ErrProtocol)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	request := b.active[sequence]
	if request == nil {
		return false, nil
	}
	if request.bindingID != bindingID || request.generation != generation ||
		request.requestID != requestID || request.sequence != sequence {
		return false, fmt.Errorf("%w: cancellation identity", ErrProtocol)
	}
	request.cancelOnce.Do(func() {
		close(request.cancel)
	})
	return true, nil
}

// CancelRequestAndWait signals one exact request and waits until its relay
// exchange has either observed a Server-terminal response or failed closed.
func (b *Broker) CancelRequestAndWait(
	ctx context.Context,
	bindingID string,
	generation uint64,
	requestID string,
	sequence uint64,
) (bool, bool, error) {
	if !validOpaqueText(bindingID, 128) || generation == 0 || !validOpaqueText(requestID, 128) || sequence == 0 {
		return false, false, fmt.Errorf("%w: cancellation identity", ErrProtocol)
	}
	b.mu.Lock()
	request := b.active[sequence]
	if request == nil {
		b.mu.Unlock()
		return false, false, nil
	}
	if request.bindingID != bindingID || request.generation != generation ||
		request.requestID != requestID || request.sequence != sequence {
		b.mu.Unlock()
		return false, false, fmt.Errorf("%w: cancellation identity", ErrProtocol)
	}
	request.cancelOnce.Do(func() {
		close(request.cancel)
	})
	forwardDone := request.forwardDone
	b.mu.Unlock()

	select {
	case <-forwardDone:
		return true, request.serverTerminalObserved, nil
	case <-ctx.Done():
		return true, false, ctx.Err()
	case <-b.closed:
		return true, false, ErrUnavailable
	}
}

// ConsumePermit atomically consumes a permit and verifies its full identity.
func (b *Broker) ConsumePermit(bindingID string, generation uint64, requestID string, sequence uint64, payloadBytes int) (Permit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	b.pruneConsumedLocked(now)
	permit := b.permits[sequence]
	if permit == nil {
		if _, seen := b.consumed[sequence]; seen {
			return Permit{}, ErrReplay
		}
		return Permit{}, ErrRevoked
	}
	if permit.BindingID != bindingID || permit.Generation != generation || permit.RequestID != requestID ||
		permit.Sequence != sequence {
		return Permit{}, fmt.Errorf("%w: permit identity", ErrProtocol)
	}
	if payloadBytes < 0 || payloadBytes > permit.MaxPayloadBytes {
		return Permit{}, fmt.Errorf("%w: payload size", ErrProtocol)
	}
	if !permit.ExpiresAt.After(now) {
		zeroPermit(permit)
		delete(b.permits, sequence)
		delete(b.active, sequence)
		b.notifyRequestChangeLocked()
		return Permit{}, ErrLeaseExpired
	}
	copyValue := *permit
	zeroPermit(permit)
	delete(b.permits, sequence)
	b.consumed[sequence] = now
	if active := b.active[sequence]; active != nil {
		active.submitted = true
		b.notifyRequestChangeLocked()
	}
	return copyValue, nil
}

// RevokeBinding clears all short-lived materials for a generation and closes its relay.
func (b *Broker) RevokeBinding(bindingID string, generation uint64) {
	var state *bindingState
	b.mu.Lock()
	if found := b.bindings[bindingID]; found != nil {
		if generation == 0 || found.metadata.Generation == generation {
			state = found
			state.metadata.Status = "revoked"
			state.metadata.LeaseHealth = "expired"
			state.metadata.LeaseGraceUntil = nil
			state.terminal = true
			state.terminalGeneration = state.metadata.Generation
			b.clearBindingMaterialsLocked(bindingID)
		}
	}
	b.mu.Unlock()
	if state != nil {
		closeRelay(state, ErrRevoked)
	}
}

// Reload clears tickets, permits, and relay connections while retaining metadata.
func (b *Broker) Reload() {
	var states []*bindingState
	b.mu.Lock()
	for id, state := range b.bindings {
		b.clearBindingMaterialsLocked(id)
		states = append(states, state)
	}
	b.mu.Unlock()
	for _, state := range states {
		closeRelay(state, ErrUnavailable)
	}
}

// RenewDue renews active bindings whose lease is near the admission boundary.
func (b *Broker) RenewDue(ctx context.Context) error {
	if !b.cfg.Enabled {
		return ErrDisabled
	}
	b.mu.RLock()
	states := make([]*bindingState, 0, len(b.bindings))
	for _, state := range b.bindings {
		states = append(states, state)
	}
	b.mu.RUnlock()
	var firstErr error
	for _, state := range states {
		b.mu.RLock()
		current := b.bindings[state.metadata.BindingID]
		if current == nil || current != state {
			b.mu.RUnlock()
			continue
		}
		metadata := cloneBinding(current.metadata)
		b.mu.RUnlock()
		if metadata.Status != "active" && metadata.Status != "expired" {
			continue
		}
		if metadata.Status == "expired" || metadata.LeaseHealth == "expired" {
			b.expireBinding(state)
			if firstErr == nil {
				firstErr = ErrLeaseExpired
			}
			continue
		}
		if metadata.AbsoluteTTLUntil != "" {
			absoluteTTL, parseErr := time.Parse(time.RFC3339Nano, metadata.AbsoluteTTLUntil)
			if parseErr != nil {
				b.expireBinding(state)
				if firstErr == nil {
					firstErr = fmt.Errorf("%w: absolute TTL timestamp", ErrProtocol)
				}
				continue
			}
			if !absoluteTTL.After(time.Now().UTC()) {
				b.expireBinding(state)
				if firstErr == nil {
					firstErr = ErrLeaseExpired
				}
				continue
			}
		}
		if metadata.LeaseHealth == "renewal_grace" {
			if metadata.LeaseGraceUntil == nil || *metadata.LeaseGraceUntil == "" {
				b.expireBinding(state)
				if firstErr == nil {
					firstErr = fmt.Errorf("%w: renewal grace timestamp", ErrProtocol)
				}
				continue
			}
			graceUntil, parseErr := time.Parse(time.RFC3339Nano, *metadata.LeaseGraceUntil)
			if parseErr != nil {
				b.expireBinding(state)
				if firstErr == nil {
					firstErr = fmt.Errorf("%w: renewal grace timestamp", ErrProtocol)
				}
				continue
			}
			if !graceUntil.After(time.Now().UTC()) {
				b.expireBinding(state)
				if firstErr == nil {
					firstErr = ErrLeaseExpired
				}
				continue
			}
			if err := b.renewBinding(ctx, state); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		if metadata.LeaseHealth != "healthy" {
			continue
		}
		due := true
		if metadata.LeaseUntil != nil && *metadata.LeaseUntil != "" {
			if leaseUntil, err := time.Parse(time.RFC3339Nano, *metadata.LeaseUntil); err == nil {
				due = leaseUntil.Sub(time.Now().UTC()) <= time.Duration(b.cfg.AdmissionMinRemainingSeconds)*time.Second
			}
		}
		if !due {
			continue
		}
		if err := b.renewBinding(ctx, state); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Serve runs the owner-only wrapper IPC endpoint until the context is canceled.
func (b *Broker) Serve(ctx context.Context) error {
	if !b.cfg.Enabled {
		return ErrDisabled
	}
	listener, err := b.listen()
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = b.Close()
	}()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			select {
			case <-b.closed:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			_ = b.handleConnection(connection)
		}()
	}
}

// Run combines the IPC server with periodic control-plane refresh and renewal.
func (b *Broker) Run(ctx context.Context) error {
	if !b.cfg.Enabled {
		return ErrDisabled
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- b.Serve(ctx) }()
	if b.cfg.ControlPlaneConfigured {
		if err := b.Refresh(ctx); err != nil {
			// Keep the local endpoint available while the control plane is
			// temporarily unavailable; admission remains fail-closed until a
			// valid snapshot is received.
			_ = b.markRenewalFailure()
		}
	}
	interval := time.Duration(b.cfg.RenewIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 20 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = b.Close()
			return ctx.Err()
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrDisabled) {
				return err
			}
			return nil
		case <-ticker.C:
			if b.cfg.ControlPlaneConfigured {
				if err := b.Refresh(ctx); err != nil {
					// A transient control-plane outage is handled by the local
					// grace policy; it must not create a new binding or replay work.
					_ = b.markRenewalFailure()
					continue
				}
				_ = b.RenewDue(ctx)
			}
		}
	}
}

// Close stops the IPC endpoint and clears all ephemeral authorization material.
func (b *Broker) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		close(b.closed)
		b.listenerMu.Lock()
		if b.listener != nil {
			closeErr = b.listener.Close()
		}
		b.listenerMu.Unlock()
		b.Reload()
		b.mu.Lock()
		clear(b.sessions)
		clear(b.nonces)
		clear(b.peerUIDs)
		b.mu.Unlock()
		b.wg.Wait()
		if b.cfg.SocketPath != "" {
			if info, err := os.Lstat(b.cfg.SocketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
				_ = os.Remove(b.cfg.SocketPath)
			}
		}
	})
	return closeErr
}

func (b *Broker) binding(bindingID string) *bindingState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if bindingID != "" {
		return b.bindings[bindingID]
	}
	if len(b.bindings) != 1 {
		return nil
	}
	for _, state := range b.bindings {
		return state
	}
	return nil
}

func (b *Broker) bindingForNonce(nonce string) (string, *bindingState, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	toolSessionID, err := b.sessionForNonceLocked(nonce)
	if err != nil {
		return "", nil, err
	}
	state, err := b.bindingForSessionLocked(toolSessionID)
	if err != nil {
		return "", nil, err
	}
	return toolSessionID, state, nil
}

func (b *Broker) sessionForNonce(nonce string) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessionForNonceLocked(nonce)
}

func (b *Broker) sessionForNonceLocked(nonce string) (string, error) {
	if !validOpaqueText(nonce, 256) {
		return "", fmt.Errorf("%w: session nonce", ErrProtocol)
	}
	toolSessionID := b.nonces[nonce]
	if toolSessionID == "" || b.sessions[toolSessionID] != nonce {
		return "", ErrUnavailable
	}
	return toolSessionID, nil
}

func (b *Broker) bindingForSessionLocked(toolSessionID string) (*bindingState, error) {
	var selected *bindingState
	for _, state := range b.bindings {
		if state.metadata.ToolSessionID != toolSessionID {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("%w: ambiguous tool session binding", ErrProtocol)
		}
		selected = state
	}
	if selected == nil {
		return nil, ErrUnavailable
	}
	return selected, nil
}

func (b *Broker) reloadToolSession(toolSessionID string) {
	var toClose []*bindingState
	b.mu.Lock()
	for bindingID, state := range b.bindings {
		if state.metadata.ToolSessionID == toolSessionID {
			b.clearBindingMaterialsLocked(bindingID)
			toClose = append(toClose, state)
		}
	}
	b.mu.Unlock()
	for _, state := range toClose {
		closeRelay(state, ErrUnavailable)
	}
}

func (b *Broker) ensureLease(ctx context.Context, state *bindingState) error {
	b.mu.RLock()
	current := b.bindings[state.metadata.BindingID]
	if current == nil || current != state {
		b.mu.RUnlock()
		return ErrUnavailable
	}
	metadata := cloneBinding(current.metadata)
	b.mu.RUnlock()
	if metadata.Status == "expired" || metadata.LeaseHealth == "expired" {
		return ErrLeaseExpired
	}
	if metadata.Status != "active" {
		return ErrUnavailable
	}
	if metadata.LeaseHealth != "healthy" {
		if metadata.LeaseHealth == "renewal_grace" && metadata.LeaseGraceUntil != nil {
			graceUntil, err := time.Parse(time.RFC3339Nano, *metadata.LeaseGraceUntil)
			if err == nil && !graceUntil.After(time.Now().UTC()) {
				b.expireBinding(state)
				return ErrLeaseExpired
			}
		}
		return ErrLeaseRenewalRequired
	}
	now := time.Now().UTC()
	if metadata.AbsoluteTTLUntil != "" {
		absoluteTTL, err := time.Parse(time.RFC3339Nano, metadata.AbsoluteTTLUntil)
		if err != nil {
			return fmt.Errorf("%w: absolute TTL timestamp", ErrProtocol)
		}
		if !absoluteTTL.After(now) {
			return ErrLeaseExpired
		}
	}
	if metadata.LeaseUntil == nil || *metadata.LeaseUntil == "" {
		if err := b.renewBinding(ctx, state); err != nil {
			return ErrLeaseRenewalRequired
		}
		return nil
	}
	leaseUntil, err := time.Parse(time.RFC3339Nano, *metadata.LeaseUntil)
	if err != nil {
		return fmt.Errorf("%w: lease timestamp", ErrProtocol)
	}
	if !leaseUntil.After(now) {
		if err := b.renewBinding(ctx, state); err != nil {
			return ErrLeaseExpired
		}
		return nil
	}
	if leaseUntil.Sub(now) < time.Duration(b.cfg.AdmissionMinRemainingSeconds)*time.Second {
		if err := b.renewBinding(ctx, state); err != nil {
			return ErrLeaseRenewalRequired
		}
	}
	return nil
}

func (b *Broker) renewBinding(ctx context.Context, state *bindingState) error {
	if !b.cfg.ControlPlaneConfigured {
		return ErrLeaseRenewalRequired
	}
	b.mu.RLock()
	current := b.bindings[state.metadata.BindingID]
	if current == nil || current != state {
		b.mu.RUnlock()
		return ErrUnavailable
	}
	metadata := cloneBinding(current.metadata)
	b.mu.RUnlock()
	if metadata.Status != "active" || metadata.Generation == 0 {
		return ErrUnavailable
	}
	digest := cloneStringPointer(metadata.LearningBundleDigest)
	response, err := b.cfg.Client.RenewEgoBrowserBinding(ctx, metadata.BindingID, api.EgoBrowserNodeRenewRequest{
		Generation: metadata.Generation, AllowlistRevision: metadata.AllowlistRevision,
		LearningBundleDigest: digest,
	})
	if err != nil {
		b.markRenewalFailureFor(state)
		return err
	}
	if response.Data.BindingID != "" && response.Data.BindingID != metadata.BindingID {
		b.markRenewalFailureFor(state)
		return fmt.Errorf("%w: renewal binding", ErrProtocol)
	}
	if response.Data.Generation != metadata.Generation || response.Data.LeaseHealth != "healthy" ||
		response.Data.LeaseUntil == nil || *response.Data.LeaseUntil == "" {
		b.markRenewalFailureFor(state)
		return fmt.Errorf("%w: renewal response", ErrProtocol)
	}
	leaseUntil, parseErr := time.Parse(time.RFC3339Nano, *response.Data.LeaseUntil)
	if parseErr != nil || !leaseUntil.After(time.Now().UTC()) {
		b.markRenewalFailureFor(state)
		return fmt.Errorf("%w: renewal lease timestamp", ErrProtocol)
	}
	absoluteTTL := response.Data.AbsoluteTTLUntil
	if absoluteTTL == "" {
		absoluteTTL = metadata.AbsoluteTTLUntil
	}
	abs, parseErr := time.Parse(time.RFC3339Nano, absoluteTTL)
	if parseErr != nil || !abs.After(time.Now().UTC()) || leaseUntil.After(abs) {
		b.markRenewalFailureFor(state)
		return fmt.Errorf("%w: renewal absolute TTL", ErrProtocol)
	}
	b.mu.Lock()
	current = b.bindings[metadata.BindingID]
	if current == nil || current != state || current.metadata.Generation != metadata.Generation || current.metadata.Status != "active" || current.terminal {
		b.mu.Unlock()
		return ErrRevoked
	}
	current.metadata.Generation = response.Data.Generation
	current.metadata.LeaseUntil = cloneStringPointer(response.Data.LeaseUntil)
	current.metadata.LeaseHealth = response.Data.LeaseHealth
	current.metadata.LeaseGraceUntil = cloneStringPointer(response.Data.LeaseGraceUntil)
	current.metadata.AbsoluteTTLUntil = absoluteTTL
	b.mu.Unlock()
	return nil
}

func (b *Broker) markRenewalFailure() error {
	b.mu.RLock()
	states := make([]*bindingState, 0, len(b.bindings))
	for _, state := range b.bindings {
		states = append(states, state)
	}
	b.mu.RUnlock()
	for _, state := range states {
		b.markRenewalFailureFor(state)
	}
	return ErrLeaseRenewalRequired
}

func (b *Broker) markRenewalFailureFor(state *bindingState) {
	closeConnection := false
	b.mu.Lock()
	current := b.bindings[state.metadata.BindingID]
	if current == nil || current != state || current.metadata.Status != "active" || current.terminal || current.metadata.LeaseHealth == "expired" {
		b.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	if current.metadata.LeaseHealth == "renewal_grace" && current.metadata.LeaseGraceUntil != nil {
		if graceUntil, err := time.Parse(time.RFC3339Nano, *current.metadata.LeaseGraceUntil); err == nil {
			if !graceUntil.After(now) {
				b.expireBindingLocked(current)
				closeConnection = true
			}
		} else {
			b.expireBindingLocked(current)
			closeConnection = true
		}
	}
	if !closeConnection {
		current.metadata.LeaseHealth = "renewal_grace"
		// The grace deadline is established by the first failure and is
		// intentionally preserved across subsequent failed renewals.
		if current.metadata.LeaseGraceUntil == nil || *current.metadata.LeaseGraceUntil == "" {
			grace := now.Add(time.Duration(b.cfg.RenewGraceSeconds) * time.Second).Format(time.RFC3339Nano)
			current.metadata.LeaseGraceUntil = &grace
		}
	}
	b.clearBindingMaterialsLocked(current.metadata.BindingID)
	b.mu.Unlock()
	if closeConnection {
		closeRelay(state, ErrLeaseExpired)
	}
}

// expireBinding marks a generation terminal and closes its relay outside the
// broker metadata lock.  A terminal generation cannot be revived by an older
// or same-generation control-plane snapshot.
func (b *Broker) expireBinding(state *bindingState) {
	changed := false
	b.mu.Lock()
	current := b.bindings[state.metadata.BindingID]
	if current == state && current.metadata.Status == "active" {
		b.expireBindingLocked(current)
		changed = true
	}
	b.mu.Unlock()
	if changed {
		closeRelay(state, ErrLeaseExpired)
	}
}

func (b *Broker) expireBindingLocked(state *bindingState) {
	state.metadata.Status = "expired"
	state.metadata.LeaseHealth = "expired"
	state.metadata.LeaseGraceUntil = nil
	state.terminal = true
	state.terminalGeneration = state.metadata.Generation
	b.clearBindingMaterialsLocked(state.metadata.BindingID)
}

func (b *Broker) nextSequenceLocked() (uint64, error) {
	if b.sequence == ^uint64(0) {
		return 0, fmt.Errorf("%w: sequence exhausted", ErrProtocol)
	}
	b.sequence++
	if err := persistSequence(b.cfg.StateRoot, b.sequence); err != nil {
		b.sequence--
		return 0, err
	}
	return b.sequence, nil
}

func (b *Broker) clearBindingMaterialsLocked(bindingID string) {
	for sequence, permit := range b.permits {
		if permit.BindingID == bindingID {
			zeroPermit(permit)
			delete(b.permits, sequence)
		}
	}
	for sequence, request := range b.active {
		if request.bindingID == bindingID {
			request.forwardDoneOnce.Do(func() {
				close(request.forwardDone)
			})
			delete(b.active, sequence)
		}
	}
	b.pruneConsumedLocked(time.Now().UTC())
	if state := b.bindings[bindingID]; state != nil {
		state.relayEpoch.Add(1)
	}
	b.notifyRequestChangeLocked()
}

// closeRelay atomically detaches a relay and fails every pending exchange.
func closeRelay(state *bindingState, reason error) {
	if state == nil {
		return
	}
	state.relayMu.Lock()
	session := state.relay
	state.relay = nil
	state.relayMu.Unlock()
	if session != nil {
		session.closeWithError(reason)
	}
}

func (b *Broker) releaseRequest(sequence uint64) {
	b.mu.Lock()
	if permit := b.permits[sequence]; permit != nil {
		zeroPermit(permit)
		delete(b.permits, sequence)
	}
	if request := b.active[sequence]; request != nil {
		request.forwardDoneOnce.Do(func() {
			close(request.forwardDone)
		})
		delete(b.active, sequence)
		b.notifyRequestChangeLocked()
	}
	b.mu.Unlock()
}

func (b *Broker) activeRequestCountLocked(bindingID string) int {
	count := 0
	for _, request := range b.active {
		if request.bindingID == bindingID {
			count++
		}
	}
	return count
}

func (b *Broker) pruneExpiredPermitsLocked(now time.Time) {
	changed := false
	for sequence, permit := range b.permits {
		if permit.ExpiresAt.After(now) {
			continue
		}
		zeroPermit(permit)
		delete(b.permits, sequence)
		if request := b.active[sequence]; request != nil && !request.submitted {
			delete(b.active, sequence)
			changed = true
		}
	}
	if changed {
		b.notifyRequestChangeLocked()
	}
}

func (b *Broker) notifyRequestChangeLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *Broker) waitForSendTurn(ctx context.Context, bindingID string, sequence uint64) error {
	tracked := false
	for {
		b.mu.Lock()
		b.pruneExpiredPermitsLocked(time.Now().UTC())
		request := b.active[sequence]
		if !tracked && request == nil {
			b.mu.Unlock()
			return nil
		}
		tracked = true
		if request == nil || request.bindingID != bindingID || !request.submitted {
			b.mu.Unlock()
			return ErrRevoked
		}
		blocked := false
		for otherSequence, other := range b.active {
			if other.bindingID == bindingID && otherSequence < sequence && !other.sent {
				blocked = true
				break
			}
		}
		if !blocked {
			b.mu.Unlock()
			return nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.closed:
			return ErrUnavailable
		case <-changed:
		}
	}
}

func (b *Broker) markRequestSent(bindingID string, sequence uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	request := b.active[sequence]
	if request == nil {
		return nil
	}
	if request.bindingID != bindingID || !request.submitted {
		return ErrRevoked
	}
	request.sent = true
	b.notifyRequestChangeLocked()
	return nil
}

func (b *Broker) requestCancellation(request outerEnvelope) (<-chan struct{}, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	active := b.active[request.Sequence]
	if active == nil {
		// Direct forwarding tests and a terminal-response race can have no local
		// registry entry. Neither case authorizes cancellation of another request.
		return nil, nil
	}
	if active.bindingID != request.BindingID || active.generation != request.Generation ||
		active.requestID != request.RequestID || active.sequence != request.Sequence || !active.submitted {
		return nil, fmt.Errorf("%w: cancellation identity", ErrProtocol)
	}
	return active.cancel, nil
}

func (b *Broker) validateForwardBinding(state *bindingState, generation uint64) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	current := b.bindings[state.metadata.BindingID]
	if current == nil || current != state || current.metadata.Generation != generation {
		return ErrRevoked
	}
	if current.metadata.Status != "active" || current.metadata.LeaseHealth != "healthy" {
		if current.metadata.Status == "expired" || current.metadata.LeaseHealth == "expired" {
			return ErrLeaseExpired
		}
		return ErrRevoked
	}
	return nil
}

func (b *Broker) pruneConsumedLocked(now time.Time) {
	const replayWindow = 10 * time.Minute
	for sequence, consumedAt := range b.consumed {
		if !consumedAt.Add(replayWindow).After(now) {
			delete(b.consumed, sequence)
		}
	}
	const maximumReplayEntries = 4096
	for len(b.consumed) > maximumReplayEntries {
		var oldestSequence uint64
		var oldest time.Time
		for sequence, consumedAt := range b.consumed {
			if oldest.IsZero() || consumedAt.Before(oldest) {
				oldestSequence, oldest = sequence, consumedAt
			}
		}
		delete(b.consumed, oldestSequence)
	}
}

func (b *Broker) validateBinding(binding api.EgoBrowserBinding) error {
	if !validOpaqueText(binding.BindingID, 128) || !validOpaqueText(binding.ToolSessionID, 128) ||
		binding.Generation == 0 || !validOpaqueText(binding.NodeID, 128) {
		return fmt.Errorf("%w: binding identity", ErrProtocol)
	}
	// The Bridge key is the only material that lets the broker wrap a request
	// session key.  Reject absent, padded, aliased, or wrong-sized encodings
	// before the binding can enter the broker snapshot.
	if !validCanonicalBase64URL(binding.EncryptionPublicKey, 32) {
		return fmt.Errorf("%w: binding encryption key", ErrProtocol)
	}
	if b.cfg.NodeID != "" && binding.NodeID != b.cfg.NodeID {
		return fmt.Errorf("%w: binding node", ErrProtocol)
	}
	if binding.ControlChannel != "ego_browser_bridge" || binding.RelayBindingKind != "ego_browser" ||
		binding.AuthorizationMode != "ego_browser_script_full_trust" || binding.AuthorizationPolicyVersion != 1 ||
		binding.RemotePlatform != "linux" || binding.LocalPlatform != "macos" {
		return fmt.Errorf("%w: binding contract", ErrProtocol)
	}
	if binding.Status != "active" && binding.Status != "connecting" && binding.Status != "pending_device" && binding.Status != "probing_local_browser" && binding.Status != "paused" && binding.Status != "expired" {
		return fmt.Errorf("%w: binding status", ErrProtocol)
	}
	if binding.MaxParallelRequests < 1 || binding.MaxParallelRequests > 4 {
		return fmt.Errorf("%w: binding parallelism", ErrProtocol)
	}
	if binding.BridgeProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: binding protocol", ErrProtocol)
	}
	if binding.AllowlistRevision == 0 || !validBindingCapabilities(binding) {
		return fmt.Errorf("%w: binding capabilities", ErrProtocol)
	}
	if binding.LeaseHealth != "healthy" && binding.LeaseHealth != "renewal_grace" && binding.LeaseHealth != "expired" {
		return fmt.Errorf("%w: binding lease health", ErrProtocol)
	}
	if binding.AbsoluteTTLUntil != "" {
		if _, err := time.Parse(time.RFC3339Nano, binding.AbsoluteTTLUntil); err != nil {
			return fmt.Errorf("%w: binding absolute TTL", ErrProtocol)
		}
	}
	return nil
}

func (b *Broker) listen() (*net.UnixListener, error) {
	b.listenerMu.Lock()
	defer b.listenerMu.Unlock()
	if b.listener != nil {
		return b.listener, nil
	}
	if err := prepareSocket(b.cfg.SocketPath); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: b.cfg.SocketPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(b.cfg.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	b.listener = listener
	return listener, nil
}

func (b *Broker) handleConnection(connection *net.UnixConn) error {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Duration(b.cfg.MaxExecuteTimeoutMS+30_000) * time.Millisecond))
	first, err := readFrame(connection)
	if err != nil {
		return err
	}
	var envelope map[string]json.RawMessage
	if err := decodeStrictJSON(first, &envelope); err != nil {
		return fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	messageType := rawString(envelope["type"])
	startupNonce := rawString(envelope["startup_nonce"])
	if err := b.authorizePeer(connection, startupNonce); err != nil {
		return err
	}
	switch messageType {
	case "permit_request":
		startedAt := time.Now()
		metricStatus := "rejected"
		requestBytes := 0
		responseBytes := 0
		defer func() {
			recordExecuteMetrics(startedAt, metricStatus, requestBytes, responseBytes)
		}()
		var request PermitRequest
		if err := decodeStrictJSON(first, &request); err != nil {
			return err
		}
		permit, err := b.IssuePermit(context.Background(), request)
		if err != nil {
			metricStatus = executeMetricStatus(err)
			return writeError(connection, "permit_response", err)
		}
		defer zeroPermit(&permit)
		defer b.releaseRequest(permit.Sequence)
		if err := writeFrame(connection, mustJSON(permitResponse(b, permit))); err != nil {
			return err
		}
		requestFrame, err := readFrame(connection)
		if err != nil {
			return err
		}
		rawOuter, outer, err := decodeSubmitOrOuter(requestFrame, request.StartupNonce)
		if err != nil {
			return writeError(connection, "execute_result", err)
		}
		requestBytes = len(rawOuter)
		if outer.BindingID != permit.BindingID || outer.Generation != permit.Generation || outer.RequestID != permit.RequestID || outer.Sequence != permit.Sequence {
			return writeError(connection, "execute_result", fmt.Errorf("%w: outer identity", ErrProtocol))
		}
		consumed, err := b.ConsumePermit(permit.BindingID, permit.Generation, permit.RequestID, permit.Sequence, outer.PayloadBytes)
		if err != nil {
			metricStatus = executeMetricStatus(err)
			return writeError(connection, "execute_result", err)
		}
		defer zeroPermit(&consumed)
		forwardContext, cancelForward := context.WithTimeout(
			context.Background(),
			time.Duration(b.cfg.MaxExecuteTimeoutMS+30_000)*time.Millisecond,
		)
		stopConnectionWatch := watchConnectionLoss(connection, cancelForward)
		response, err := b.forward(forwardContext, permit.BindingID, rawOuter, consumed)
		stopConnectionWatch()
		cancelForward()
		if err != nil {
			metricStatus = executeMetricStatus(err)
			return writeError(connection, "execute_result", err)
		}
		responseBytes = len(response)
		if err := writeFrame(connection, response); err != nil {
			metricStatus = "unknown_result"
			return err
		}
		metricStatus = "completed"
		return nil
	case "doctor", "reload":
		var request controlRequest
		if err := decodeStrictJSON(first, &request); err != nil {
			return err
		}
		if request.Protocol != ProtocolVersion || request.Type != messageType {
			return writeError(connection, messageType+"_response", fmt.Errorf("%w: startup nonce", ErrProtocol))
		}
		toolSessionID, err := b.sessionForNonce(request.StartupNonce)
		if err != nil {
			return writeError(connection, messageType+"_response", err)
		}
		if messageType == "reload" {
			b.reloadToolSession(toolSessionID)
		}
		return writeFrame(connection, mustJSON(controlResponse(b, messageType, toolSessionID)))
	default:
		return writeError(connection, "error", fmt.Errorf("%w: unsupported command", ErrProtocol))
	}
}

func watchConnectionLoss(connection *net.UnixConn, cancel context.CancelFunc) func() {
	stop := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		_, _ = readFrame(connection)
		select {
		case <-stop:
			return
		default:
			cancel()
		}
	}()
	return func() {
		close(stop)
		_ = connection.SetReadDeadline(time.Now())
		<-exited
	}
}

func executeMetricStatus(err error) string {
	switch {
	case errors.Is(err, ErrConcurrency):
		return "concurrency_conflict"
	case errors.Is(err, ErrLeaseRenewalRequired):
		return "renewal_required"
	case errors.Is(err, ErrLeaseExpired):
		return "lease_expired"
	case errors.Is(err, ErrRevoked), errors.Is(err, ErrUnavailable), errors.Is(err, ErrDisabled):
		return "unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrProtocol):
		return "rejected"
	default:
		return "unknown_result"
	}
}

func recordExecuteMetrics(startedAt time.Time, status string, requestBytes int, responseBytes int) {
	duration := time.Since(startedAt)
	log.Printf("metric=ego_browser_execute_total value=1 unit=executions status=%s", status)
	log.Printf(
		"metric=ego_browser_execute_duration_seconds value=%.6f unit=seconds status=%s",
		duration.Seconds(),
		status,
	)
	log.Printf(
		"metric=ego_browser_bytes_total value=%d unit=bytes direction=request status=%s",
		requestBytes,
		status,
	)
	log.Printf(
		"metric=ego_browser_bytes_total value=%d unit=bytes direction=response status=%s",
		responseBytes,
		status,
	)
}

// authorizePeer requires both the exact process-local session capability and
// the runtime UID that the privileged helper reported for that session.
func (b *Broker) authorizePeer(connection *net.UnixConn, startupNonce string) error {
	uid, err := peerUID(connection)
	if err != nil {
		return fmt.Errorf("%w: peer credentials", ErrProtocol)
	}
	return b.authorizePeerUID(startupNonce, uid)
}

func (b *Broker) authorizePeerUID(startupNonce string, uid uint32) error {
	b.mu.RLock()
	toolSessionID := b.nonces[startupNonce]
	expectedUID, authorized := b.peerUIDs[toolSessionID]
	b.mu.RUnlock()
	if toolSessionID == "" || !authorized || uid != expectedUID {
		return fmt.Errorf("%w: peer uid", ErrProtocol)
	}
	return nil
}

func grantPeerSocketAccess(socketPath string, uid uint32) error {
	if uid == uint32(os.Getuid()) || uid == 0 {
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%w: cross-user socket ACL is unsupported", ErrProtocol)
	}
	info, err := os.Lstat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: broker socket metadata", ErrProtocol)
	}
	parent := filepath.Dir(socketPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: broker socket directory", ErrProtocol)
	}
	principal := fmt.Sprintf("u:%d", uid)
	for _, access := range [][2]string{
		{parent, principal + ":--x"},
		{socketPath, principal + ":rw-"},
	} {
		command := exec.Command("setfacl", "-m", access[1], access[0])
		if err := command.Run(); err != nil {
			return fmt.Errorf("%w: broker socket ACL", ErrProtocol)
		}
	}
	return nil
}

type controlRequest struct {
	Protocol     string          `json:"protocol"`
	Type         string          `json:"type"`
	StartupNonce string          `json:"startup_nonce"`
	Payload      json.RawMessage `json:"payload"`
}

type outerEnvelope struct {
	Protocol         string `json:"protocol"`
	Channel          string `json:"channel"`
	RelayBindingKind string `json:"relay_binding_kind"`
	Type             string `json:"type"`
	RequestID        string `json:"request_id"`
	BindingID        string `json:"binding_id"`
	Generation       uint64 `json:"generation"`
	Sequence         uint64 `json:"sequence"`
	Direction        string `json:"direction"`
	PayloadBytes     int    `json:"payload_bytes"`
	Nonce            string `json:"nonce"`
	Ciphertext       string `json:"ciphertext"`
	AuthTag          string `json:"auth_tag"`
	KeyWrap          string `json:"key_wrap"`
}

type innerCancelRequest struct {
	Protocol  string `json:"protocol"`
	RequestID string `json:"request_id"`
	Sequence  uint64 `json:"sequence"`
	Type      string `json:"type"`
}

type submitRequest struct {
	Protocol     string          `json:"protocol"`
	Type         string          `json:"type"`
	StartupNonce string          `json:"startup_nonce"`
	Envelope     json.RawMessage `json:"envelope"`
}

func decodeSubmitOrOuter(frame []byte, startupNonce string) ([]byte, outerEnvelope, error) {
	var probe map[string]json.RawMessage
	if err := decodeStrictJSON(frame, &probe); err != nil {
		return nil, outerEnvelope{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if rawString(probe["type"]) == "submit" {
		var submit submitRequest
		if err := decodeStrictJSON(frame, &submit); err != nil {
			return nil, outerEnvelope{}, fmt.Errorf("%w: %v", ErrProtocol, err)
		}
		if submit.Protocol != ProtocolVersion || submit.StartupNonce != startupNonce || len(submit.Envelope) == 0 {
			return nil, outerEnvelope{}, fmt.Errorf("%w: submit metadata", ErrProtocol)
		}
		frame = submit.Envelope
	}
	var outer outerEnvelope
	if err := decodeStrictJSON(frame, &outer); err != nil {
		return nil, outerEnvelope{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if err := validateOuter(outer, len(frame)); err != nil {
		return nil, outerEnvelope{}, err
	}
	return frame, outer, nil
}

func validateOuter(outer outerEnvelope, wireBytes int) error {
	if outer.Protocol != ProtocolVersion || outer.Channel != "ego_browser_bridge" || outer.RelayBindingKind != "ego_browser" ||
		outer.Type != "execute" || outer.Direction != "request" || !validOpaqueText(outer.RequestID, 128) ||
		!validOpaqueText(outer.BindingID, 128) || outer.Generation == 0 || outer.Sequence == 0 || outer.PayloadBytes <= 0 ||
		wireBytes > defaultMaxPayloadBytes {
		return fmt.Errorf("%w: outer metadata", ErrProtocol)
	}
	// The wrapper may submit only a placeholder.  The broker is the sole
	// component allowed to construct the authenticated key wrap.
	if outer.KeyWrap != "" {
		return fmt.Errorf("%w: wrapper key wrap", ErrProtocol)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(outer.Nonce)
	if err != nil || len(nonce) != 12 || base64.RawURLEncoding.EncodeToString(nonce) != outer.Nonce {
		return fmt.Errorf("%w: nonce", ErrProtocol)
	}
	tag, err := base64.RawURLEncoding.DecodeString(outer.AuthTag)
	if err != nil || len(tag) != 16 || base64.RawURLEncoding.EncodeToString(tag) != outer.AuthTag {
		return fmt.Errorf("%w: auth tag", ErrProtocol)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(outer.Ciphertext)
	if err != nil || len(ciphertext) != outer.PayloadBytes || base64.RawURLEncoding.EncodeToString(ciphertext) != outer.Ciphertext {
		return fmt.Errorf("%w: ciphertext", ErrProtocol)
	}
	return nil
}

// validateRelayResponse checks only the server-visible response metadata.  The
// payload remains opaque to the Node, but a peer must not be able to swap a
// response from another binding, generation, or request onto this socket.
func validateRelayResponse(response, request outerEnvelope) error {
	if response.Protocol != ProtocolVersion || response.Channel != "ego_browser_bridge" ||
		response.RelayBindingKind != "ego_browser" || response.Type != "execute_result" ||
		response.Direction != "response" || response.RequestID != request.RequestID ||
		response.BindingID != request.BindingID || response.Generation != request.Generation ||
		response.Sequence != request.Sequence {
		return fmt.Errorf("%w: relay response identity", ErrProtocol)
	}
	return validateOuterPayload(response, defaultMaxPayloadBytes)
}

func validateOuterPayload(outer outerEnvelope, wireBytesLimit int) error {
	if !validOpaqueText(outer.RequestID, 128) || !validOpaqueText(outer.BindingID, 128) ||
		outer.Generation == 0 || outer.Sequence == 0 || outer.PayloadBytes <= 0 {
		return fmt.Errorf("%w: outer payload metadata", ErrProtocol)
	}
	if wireBytesLimit <= 0 {
		return fmt.Errorf("%w: outer payload limit", ErrProtocol)
	}
	if outer.KeyWrap != "" {
		return fmt.Errorf("%w: response key wrap", ErrProtocol)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(outer.Nonce)
	if err != nil || len(nonce) != 12 || base64.RawURLEncoding.EncodeToString(nonce) != outer.Nonce {
		return fmt.Errorf("%w: nonce", ErrProtocol)
	}
	tag, err := base64.RawURLEncoding.DecodeString(outer.AuthTag)
	if err != nil || len(tag) != 16 || base64.RawURLEncoding.EncodeToString(tag) != outer.AuthTag {
		return fmt.Errorf("%w: auth tag", ErrProtocol)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(outer.Ciphertext)
	if err != nil || len(ciphertext) != outer.PayloadBytes || base64.RawURLEncoding.EncodeToString(ciphertext) != outer.Ciphertext {
		return fmt.Errorf("%w: ciphertext", ErrProtocol)
	}
	return nil
}

func cancelFrame(request outerEnvelope, sessionKey [32]byte) ([]byte, error) {
	plaintext, err := json.Marshal(innerCancelRequest{
		Protocol: InnerProtocolVersion, RequestID: request.RequestID,
		Sequence: request.Sequence, Type: "cancel",
	})
	if err != nil {
		return nil, fmt.Errorf("%w: cancel payload", ErrProtocol)
	}
	cancel := request
	cancel.Type = "cancel"
	cancel.Direction = "request"
	cancel.PayloadBytes = len(plaintext)
	cancel.KeyWrap = ""
	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("%w: cancel nonce", ErrProtocol)
	}
	cancel.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	aad, err := outerAAD(cancel)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(sessionKey[:])
	if err != nil {
		return nil, fmt.Errorf("%w: cancel cipher", ErrProtocol)
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad)
	if len(sealed) != len(plaintext)+chacha20poly1305.Overhead {
		return nil, fmt.Errorf("%w: cancel ciphertext", ErrProtocol)
	}
	cancel.Ciphertext = base64.RawURLEncoding.EncodeToString(sealed[:len(plaintext)])
	cancel.AuthTag = base64.RawURLEncoding.EncodeToString(sealed[len(plaintext):])
	frame := mustJSON(cancel)
	if err := validateCancelOuter(cancel, len(frame)); err != nil {
		return nil, err
	}
	return frame, nil
}

func outerAAD(envelope outerEnvelope) ([]byte, error) {
	aad, err := json.Marshal(map[string]any{
		"binding_id":         envelope.BindingID,
		"channel":            envelope.Channel,
		"direction":          envelope.Direction,
		"generation":         envelope.Generation,
		"payload_bytes":      envelope.PayloadBytes,
		"protocol":           envelope.Protocol,
		"relay_binding_kind": envelope.RelayBindingKind,
		"request_id":         envelope.RequestID,
		"sequence":           envelope.Sequence,
		"type":               envelope.Type,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: outer AAD", ErrProtocol)
	}
	return aad, nil
}

func validateCancelOuter(cancel outerEnvelope, wireBytes int) error {
	if cancel.Protocol != ProtocolVersion || cancel.Channel != "ego_browser_bridge" ||
		cancel.RelayBindingKind != "ego_browser" || cancel.Type != "cancel" ||
		cancel.Direction != "request" || cancel.KeyWrap != "" ||
		wireBytes <= 0 || wireBytes > defaultMaxPayloadBytes {
		return fmt.Errorf("%w: cancel outer metadata", ErrProtocol)
	}
	return validateOuterPayload(cancel, defaultMaxPayloadBytes)
}

func newRelaySession(connection *websocket.Conn) *relaySession {
	return &relaySession{
		connection: connection,
		pending:    make(map[relayRequestKey]pendingRelayRequest),
		abandoned:  make(map[relayRequestKey]struct{}),
	}
}

func relayKey(envelope outerEnvelope) relayRequestKey {
	return relayRequestKey{
		bindingID:  envelope.BindingID,
		generation: envelope.Generation,
		requestID:  envelope.RequestID,
		sequence:   envelope.Sequence,
	}
}

func (session *relaySession) register(request outerEnvelope) (<-chan relayResult, error) {
	key := relayKey(request)
	result := make(chan relayResult, 1)
	session.pendingMu.Lock()
	defer session.pendingMu.Unlock()
	if session.closed.Load() {
		return nil, ErrUnavailable
	}
	if _, exists := session.pending[key]; exists {
		return nil, fmt.Errorf("%w: duplicate pending relay request", ErrProtocol)
	}
	if _, exists := session.abandoned[key]; exists {
		return nil, fmt.Errorf("%w: reused abandoned relay request", ErrProtocol)
	}
	session.pending[key] = pendingRelayRequest{request: request, result: result}
	return result, nil
}

func (session *relaySession) abandon(key relayRequestKey, result <-chan relayResult) error {
	session.pendingMu.Lock()
	defer session.pendingMu.Unlock()
	pending, exists := session.pending[key]
	if !exists || pending.result != result {
		return nil
	}
	delete(session.pending, key)
	if len(session.abandoned) >= maximumAbandonedRelayRequests {
		return fmt.Errorf("%w: too many abandoned relay requests", ErrProtocol)
	}
	session.abandoned[key] = struct{}{}
	return nil
}

func (session *relaySession) deliver(response outerEnvelope, frame []byte) error {
	key := relayKey(response)
	session.pendingMu.Lock()
	if _, abandoned := session.abandoned[key]; abandoned {
		delete(session.abandoned, key)
		session.pendingMu.Unlock()
		return nil
	}
	pending, exists := session.pending[key]
	if !exists {
		session.pendingMu.Unlock()
		return fmt.Errorf("%w: unexpected relay response", ErrProtocol)
	}
	if err := validateRelayResponse(response, pending.request); err != nil {
		session.pendingMu.Unlock()
		return err
	}
	delete(session.pending, key)
	session.pendingMu.Unlock()
	pending.result <- relayResult{frame: frame}
	return nil
}

func (session *relaySession) send(ctx context.Context, frame []byte) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	if session.closed.Load() {
		return ErrUnavailable
	}
	deadline := time.Now().Add(15 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = session.connection.SetWriteDeadline(deadline)
	err := binaryRelayMessage.Send(session.connection, frame)
	_ = session.connection.SetWriteDeadline(time.Time{})
	return err
}

func (session *relaySession) closeWithError(reason error) {
	if reason == nil {
		reason = ErrUnavailable
	}
	session.closeOnce.Do(func() {
		session.closed.Store(true)
		session.pendingMu.Lock()
		pending := make([]pendingRelayRequest, 0, len(session.pending))
		for _, request := range session.pending {
			pending = append(pending, request)
		}
		clear(session.pending)
		clear(session.abandoned)
		session.pendingMu.Unlock()
		for _, request := range pending {
			request.result <- relayResult{err: reason}
		}
		_ = session.connection.Close()
	})
}

func validateRelayResponseShape(response outerEnvelope) error {
	if response.Protocol != ProtocolVersion || response.Channel != "ego_browser_bridge" ||
		response.RelayBindingKind != "ego_browser" || response.Type != "execute_result" ||
		response.Direction != "response" {
		return fmt.Errorf("%w: relay response metadata", ErrProtocol)
	}
	return validateOuterPayload(response, defaultMaxPayloadBytes)
}

func (b *Broker) readRelay(state *bindingState, session *relaySession) {
	for {
		var frame []byte
		if err := binaryRelayMessage.Receive(session.connection, &frame); err != nil {
			b.failRelay(state, session, err)
			return
		}
		if len(frame) == 0 || len(frame) > defaultMaxPayloadBytes {
			b.failRelay(state, session, fmt.Errorf("%w: relay response size", ErrProtocol))
			return
		}
		var response outerEnvelope
		if err := decodeStrictJSON(frame, &response); err != nil {
			b.failRelay(state, session, fmt.Errorf("%w: relay response envelope", ErrProtocol))
			return
		}
		if err := validateRelayResponseShape(response); err != nil {
			b.failRelay(state, session, err)
			return
		}
		if err := session.deliver(response, frame); err != nil {
			b.failRelay(state, session, err)
			return
		}
	}
}

func (b *Broker) failRelay(state *bindingState, session *relaySession, reason error) {
	state.relayMu.Lock()
	if state.relay == session {
		state.relay = nil
	}
	state.relayMu.Unlock()
	session.closeWithError(reason)
}

func (b *Broker) ensureRelaySession(
	ctx context.Context,
	state *bindingState,
	bindingID string,
	generation uint64,
	epoch uint64,
) (*relaySession, error) {
	state.relayMu.Lock()
	defer state.relayMu.Unlock()
	if state.relayEpoch.Load() != epoch {
		return nil, ErrRevoked
	}
	if state.relay != nil && !state.relay.closed.Load() {
		return state.relay, nil
	}
	select {
	case <-b.closed:
		return nil, ErrUnavailable
	default:
	}
	if !b.cfg.ControlPlaneConfigured {
		return nil, ErrUnavailable
	}
	ticket, err := b.cfg.Client.IssueEgoBrowserRelayTicket(
		ctx,
		bindingID,
		api.EgoBrowserRelayTicketRequest{Generation: generation},
	)
	if err != nil {
		return nil, err
	}
	if ticket.Data.Role != "wrapper" || ticket.Data.RelayBindingKind != "ego_browser" ||
		ticket.Data.RelayPath == "" || ticket.Data.RelayTicket == "" ||
		ticket.Data.Generation != generation {
		return nil, fmt.Errorf("%w: relay ticket", ErrProtocol)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, ticket.Data.ExpiresAt)
	if err != nil || !expiresAt.After(time.Now().UTC()) {
		return nil, ErrLeaseExpired
	}
	relay, err := b.cfg.Client.OpenEgoBrowserRelay(
		ctx,
		bindingID,
		ticket.Data.RelayPath,
		ticket.Data.RelayTicket,
	)
	if err != nil {
		return nil, err
	}
	connection, ok := relay.(*websocket.Conn)
	if !ok {
		_ = relay.Close()
		return nil, fmt.Errorf("%w: relay transport", ErrProtocol)
	}
	connection.PayloadType = websocket.BinaryFrame
	connection.MaxPayloadBytes = defaultMaxPayloadBytes
	if state.relayEpoch.Load() != epoch {
		_ = connection.Close()
		return nil, ErrRevoked
	}
	select {
	case <-b.closed:
		_ = connection.Close()
		return nil, ErrUnavailable
	default:
	}
	session := newRelaySession(connection)
	state.relay = session
	go b.readRelay(state, session)
	return session, nil
}

func (b *Broker) forward(ctx context.Context, bindingID string, frame []byte, permit Permit) (responseFrame []byte, returnErr error) {
	defer func() {
		b.finishRequestForward(permit.Sequence, returnErr == nil)
	}()
	state := b.binding(bindingID)
	if state == nil {
		return nil, ErrUnavailable
	}
	var request outerEnvelope
	if err := decodeStrictJSON(frame, &request); err != nil {
		return nil, fmt.Errorf("%w: request envelope", ErrProtocol)
	}
	if err := validateOuter(request, len(frame)); err != nil {
		return nil, err
	}
	b.mu.RLock()
	current := b.bindings[bindingID]
	if current == nil || current != state {
		b.mu.RUnlock()
		return nil, ErrRevoked
	}
	metadata := cloneBinding(current.metadata)
	b.mu.RUnlock()
	if metadata.Status != "active" || metadata.LeaseHealth != "healthy" {
		if metadata.Status == "expired" || metadata.LeaseHealth == "expired" {
			return nil, ErrLeaseExpired
		}
		return nil, ErrRevoked
	}
	wrapped, err := wrapSessionKey(
		permit.SessionKey,
		metadata.EncryptionPublicKey,
		request.BindingID,
		request.Generation,
		request.RequestID,
		request.Sequence,
	)
	if err != nil {
		return nil, err
	}
	request.KeyWrap = wrapped
	frame = mustJSON(request)
	if err := validateWrappedOuter(request, len(frame)); err != nil {
		return nil, err
	}
	requestCancellation, err := b.requestCancellation(request)
	if err != nil {
		return nil, err
	}
	epoch := state.relayEpoch.Load()
	if err := b.waitForSendTurn(ctx, bindingID, request.Sequence); err != nil {
		return nil, err
	}
	if err := b.validateForwardBinding(state, metadata.Generation); err != nil {
		return nil, err
	}
	session, err := b.ensureRelaySession(
		ctx,
		state,
		bindingID,
		metadata.Generation,
		epoch,
	)
	if err != nil {
		return nil, err
	}
	key := relayKey(request)
	result, err := session.register(request)
	if err != nil {
		return nil, err
	}
	if err := session.send(ctx, frame); err != nil {
		b.failRelay(state, session, err)
		return nil, err
	}
	if err := b.markRequestSent(bindingID, request.Sequence); err != nil {
		b.failRelay(state, session, err)
		return nil, err
	}
	cancelSent := false
	for {
		select {
		case response := <-result:
			return response.frame, response.err
		case <-requestCancellation:
			// Prefer a terminal response that was already dispatched before the
			// cancellation signal won this select.
			select {
			case response := <-result:
				return response.frame, response.err
			default:
			}
			if !cancelSent {
				if err := sendCancelFrame(session, request, permit.SessionKey); err != nil {
					b.failRelay(state, session, err)
					return nil, err
				}
				cancelSent = true
			}
			requestCancellation = nil
		case <-ctx.Done():
			select {
			case response := <-result:
				return response.frame, response.err
			default:
			}
			if !cancelSent {
				if err := sendCancelFrame(session, request, permit.SessionKey); err != nil {
					b.failRelay(state, session, err)
				}
			}
			if err := session.abandon(key, result); err != nil {
				b.failRelay(state, session, err)
			}
			return nil, ctx.Err()
		case <-b.closed:
			if err := session.abandon(key, result); err != nil {
				b.failRelay(state, session, err)
			}
			return nil, ErrUnavailable
		}
	}
}

func (b *Broker) finishRequestForward(sequence uint64, serverTerminalObserved bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	request := b.active[sequence]
	if request == nil {
		return
	}
	request.forwardDoneOnce.Do(func() {
		request.serverTerminalObserved = serverTerminalObserved
		close(request.forwardDone)
	})
}

func sendCancelFrame(session *relaySession, request outerEnvelope, sessionKey [32]byte) error {
	frame, err := cancelFrame(request, sessionKey)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.send(ctx, frame); err != nil {
		return fmt.Errorf("send request cancellation: %w", err)
	}
	return nil
}

func validateWrappedOuter(outer outerEnvelope, wireBytes int) error {
	if outer.KeyWrap == "" {
		return fmt.Errorf("%w: missing key wrap", ErrProtocol)
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(outer.KeyWrap)
	if err != nil || len(wrapped) != keyWrapBytes || base64.RawURLEncoding.EncodeToString(wrapped) != outer.KeyWrap {
		return fmt.Errorf("%w: key wrap", ErrProtocol)
	}
	// The shared payload validator is also used for responses, where key_wrap
	// must be empty. The request-specific key wrap was validated above.
	outer.KeyWrap = ""
	return validateOuterPayload(outer, wireBytes)
}

// wrapSessionKey encrypts one permit's ephemeral AEAD key to the registered
// Bridge X25519 key.  The transcript is deliberately identical to the Rust
// protocol implementation so the server never needs to handle plaintext keys.
func wrapSessionKey(sessionKey [32]byte, recipientEncoded, bindingID string, generation uint64, requestID string, sequence uint64) (string, error) {
	if recipientEncoded == "" {
		return "", fmt.Errorf("%w: missing Bridge encryption key", ErrProtocol)
	}
	recipientBytes, err := base64.RawURLEncoding.DecodeString(recipientEncoded)
	if err != nil || len(recipientBytes) != 32 || base64.RawURLEncoding.EncodeToString(recipientBytes) != recipientEncoded {
		return "", fmt.Errorf("%w: Bridge encryption key", ErrProtocol)
	}
	recipient, err := ecdh.X25519().NewPublicKey(recipientBytes)
	if err != nil {
		return "", fmt.Errorf("%w: Bridge encryption key", ErrProtocol)
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("%w: ephemeral key", ErrProtocol)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return "", fmt.Errorf("%w: key agreement", ErrProtocol)
	}
	transcript := keyWrapTranscript(bindingID, generation, sequence, requestID)
	wrapKey := sha256.Sum256(append(append([]byte("agent-remote/ego-browser/key-wrap-key/v1\x00"), shared...), transcript...))
	aead, err := chacha20poly1305.New(wrapKey[:])
	if err != nil {
		return "", fmt.Errorf("%w: key cipher", ErrProtocol)
	}
	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("%w: key nonce", ErrProtocol)
	}
	encrypted := aead.Seal(nil, nonce, sessionKey[:], transcript)
	if len(encrypted) != 48 {
		return "", fmt.Errorf("%w: key wrap size", ErrProtocol)
	}
	wrapped := make([]byte, 0, keyWrapBytes)
	wrapped = append(wrapped, ephemeral.PublicKey().Bytes()...)
	wrapped = append(wrapped, nonce...)
	wrapped = append(wrapped, encrypted...)
	return base64.RawURLEncoding.EncodeToString(wrapped), nil
}

func keyWrapTranscript(bindingID string, generation, sequence uint64, requestID string) []byte {
	transcript := bytes.NewBuffer(make([]byte, 0, 64+len(bindingID)+len(requestID)))
	transcript.WriteString("agent-remote/ego-browser/key-wrap/v1\x00")
	transcript.WriteString(bindingID)
	transcript.WriteByte(0)
	_ = binary.Write(transcript, binary.BigEndian, generation)
	_ = binary.Write(transcript, binary.BigEndian, sequence)
	transcript.WriteString(requestID)
	return transcript.Bytes()
}

type permitResponseWire struct {
	Protocol   string               `json:"protocol"`
	Type       string               `json:"type"`
	Status     string               `json:"status"`
	Permit     permitWire           `json:"permit"`
	SessionKey string               `json:"session_key"`
	Capability bridgeCapabilityWire `json:"capability"`
}

// permitWire mirrors the shared Rust RequestPermit schema. It is kept local
// to the broker because it contains ephemeral authorization material rather
// than control-plane API state.
type permitWire struct {
	BindingID            string  `json:"binding_id"`
	Generation           uint64  `json:"generation"`
	RequestID            string  `json:"request_id"`
	Sequence             uint64  `json:"sequence"`
	ExpiresAtUnixMS      uint64  `json:"expires_at_unix_ms"`
	MaxPayloadBytes      int     `json:"max_payload_bytes"`
	MaxScriptBytes       int     `json:"max_script_bytes"`
	DefaultTaskSpace     string  `json:"default_task_space"`
	AllowlistRevision    uint64  `json:"allowlist_revision"`
	LearningBundleDigest *string `json:"learning_bundle_digest"`
	ConcurrencyMode      string  `json:"concurrency_mode"`
	TaskSpaceScope       *string `json:"task_space_scope"`
	TabScope             *string `json:"tab_scope"`
}

type bridgeCapabilityWire struct {
	BridgeProtocolVersion   string   `json:"bridge_protocol_version"`
	RemoteWrapperVersion    string   `json:"remote_wrapper_version"`
	LocalRuntimeVersion     string   `json:"local_ego_browser_runtime_version"`
	EgoLiteRuntimeVersion   string   `json:"ego_lite_runtime_version"`
	SkillVersion            string   `json:"skill_version"`
	ReleaseProfile          string   `json:"release_profile"`
	SignerCertificateSHA256 string   `json:"signer_certificate_sha256"`
	CredentialProfile       string   `json:"credential_profile"`
	AllowlistRevision       uint64   `json:"allowlist_revision"`
	AllowlistRootsDigest    *string  `json:"allowlist_roots_digest"`
	LearningBundleDigest    *string  `json:"learning_bundle_digest"`
	MaxParallelRequests     int      `json:"max_parallel_requests"`
	MaxScriptBytes          int      `json:"max_script_bytes"`
	MaxExecuteTimeoutMS     int      `json:"max_execute_timeout_ms"`
	SupportedConcurrency    []string `json:"supported_concurrency"`
	Capabilities            []string `json:"capabilities"`
	RemotePlatform          string   `json:"remote_platform"`
	LocalPlatform           string   `json:"local_platform"`
}

func permitResponse(b *Broker, permit Permit) permitResponseWire {
	return permitResponseWire{
		Protocol: ProtocolVersion, Type: "permit_response", Status: "ok",
		Permit: permitWire{
			BindingID: permit.BindingID, Generation: permit.Generation, RequestID: permit.RequestID,
			Sequence: permit.Sequence, ExpiresAtUnixMS: uint64(permit.ExpiresAt.UnixMilli()),
			MaxPayloadBytes: permit.MaxPayloadBytes, MaxScriptBytes: permit.MaxScriptBytes,
			DefaultTaskSpace:  permit.DefaultTaskSpace,
			AllowlistRevision: permit.AllowlistRevision, LearningBundleDigest: permit.LearningBundleDigest,
			ConcurrencyMode: permit.ConcurrencyMode, TaskSpaceScope: optionalString(permit.TaskSpaceScope), TabScope: optionalString(permit.TabScope),
		},
		SessionKey: base64.RawURLEncoding.EncodeToString(permit.SessionKey[:]),
		Capability: b.capabilityWire(permit.BindingID),
	}
}

func (b *Broker) capabilityWire(bindingID string) bridgeCapabilityWire {
	b.mu.RLock()
	state := b.bindings[bindingID]
	if state == nil {
		b.mu.RUnlock()
		return bridgeCapabilityWire{}
	}
	metadata := cloneBinding(state.metadata)
	b.mu.RUnlock()
	return bridgeCapabilityWire{
		BridgeProtocolVersion:   ProtocolVersion,
		RemoteWrapperVersion:    b.cfg.WrapperVersion,
		LocalRuntimeVersion:     deref(metadata.LocalRuntimeVersion),
		EgoLiteRuntimeVersion:   deref(metadata.EgoLiteRuntimeVersion),
		SkillVersion:            deref(metadata.SkillVersion),
		ReleaseProfile:          metadata.ReleaseProfile,
		SignerCertificateSHA256: metadata.SignerCertificateSHA256,
		CredentialProfile:       metadata.CredentialProfile,
		AllowlistRevision:       metadata.AllowlistRevision,
		AllowlistRootsDigest:    cloneStringPointer(metadata.AllowlistRootsDigest),
		LearningBundleDigest:    cloneStringPointer(metadata.LearningBundleDigest),
		MaxParallelRequests:     minInt(b.cfg.MaxParallelRequests, metadata.MaxParallelRequests),
		MaxScriptBytes:          b.cfg.MaxScriptBytes,
		MaxExecuteTimeoutMS:     b.cfg.MaxExecuteTimeoutMS,
		SupportedConcurrency:    []string{"task_space_tab", "task_space", "binding"},
		Capabilities:            append([]string(nil), metadata.Capabilities...),
		RemotePlatform:          "linux", LocalPlatform: "macos",
	}
}

type controlResponseWire struct {
	Protocol string         `json:"protocol"`
	Type     string         `json:"type"`
	Status   string         `json:"status"`
	Response map[string]any `json:"response"`
}

func controlResponse(b *Broker, messageType, toolSessionID string) controlResponseWire {
	b.mu.RLock()
	items := make([]map[string]any, 0, 1)
	for _, state := range b.bindings {
		if state.metadata.ToolSessionID != toolSessionID {
			continue
		}
		items = append(items, map[string]any{
			"binding_id":   state.metadata.BindingID,
			"generation":   state.metadata.Generation,
			"status":       state.metadata.Status,
			"lease_health": state.metadata.LeaseHealth,
		})
	}
	b.mu.RUnlock()
	return controlResponseWire{Protocol: ProtocolVersion, Type: messageType + "_response", Status: "ok", Response: map[string]any{"bindings": items}}
}

func writeError(connection *net.UnixConn, messageType string, err error) error {
	code := "bridge_unavailable"
	switch {
	case errors.Is(err, ErrLeaseRenewalRequired):
		code = "lease_renewal_required"
	case errors.Is(err, ErrLeaseExpired):
		code = "lease_expired"
	case errors.Is(err, ErrRevoked):
		code = "binding_revoked"
	case errors.Is(err, ErrReplay):
		code = "protocol_error"
	case errors.Is(err, ErrConcurrency):
		code = "concurrency_conflict"
	case errors.Is(err, ErrProtocol):
		code = "protocol_error"
	case errors.Is(err, ErrDisabled):
		code = "bridge_unavailable"
	}
	payload := map[string]any{"protocol": ProtocolVersion, "type": messageType, "status": "error", "error": code}
	return writeFrame(connection, mustJSON(payload))
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func rawString(value json.RawMessage) string {
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}

func normalizeScope(mode, taskSpace, tab string) (scope struct{ mode, taskSpace, tab string }, err error) {
	mode = strings.TrimSpace(mode)
	taskSpace = strings.TrimSpace(taskSpace)
	tab = strings.TrimSpace(tab)
	if mode == "" || mode == "binding" {
		return struct{ mode, taskSpace, tab string }{mode: "binding"}, nil
	}
	if mode != "task_space" && mode != "task_space_tab" || taskSpace == "" || strings.ContainsAny(taskSpace, "*\r\n") {
		return struct{ mode, taskSpace, tab string }{mode: "binding"}, nil
	}
	if mode == "task_space_tab" && (tab == "" || strings.ContainsAny(tab, "*\r\n")) {
		return struct{ mode, taskSpace, tab string }{mode: "binding"}, nil
	}
	if !validOpaqueText(taskSpace, 256) || (tab != "" && !validOpaqueText(tab, 256)) {
		return struct{ mode, taskSpace, tab string }{}, fmt.Errorf("%w: scope", ErrProtocol)
	}
	return struct{ mode, taskSpace, tab string }{mode: mode, taskSpace: taskSpace, tab: tab}, nil
}

func cloneBinding(value api.EgoBrowserBinding) api.EgoBrowserBinding {
	value.Capabilities = append([]string(nil), value.Capabilities...)
	return value
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func sameStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func deref(value *string) string {
	if value == nil {
		return "unknown"
	}
	return *value
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	copyValue := value
	return &copyValue
}

func nonZeroInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func zeroPermit(permit *Permit) {
	for index := range permit.SessionKey {
		permit.SessionKey[index] = 0
	}
	permit.LearningBundleDigest = nil
}

func validBindingCapabilities(binding api.EgoBrowserBinding) bool {
	seen := make(map[string]struct{}, len(binding.Capabilities))
	for _, capability := range binding.Capabilities {
		switch capability {
		case "ego_browser_script_execute_v1", "ego_browser_snapshot_v1", "ego_browser_screenshot_artifact_v1",
			"ego_browser_task_space_v1", "ego_browser_concurrency_v1", "ego_browser_file_allowlist_v1",
			"ego_browser_site_learning_v1":
		default:
			return false
		}
		if _, exists := seen[capability]; exists {
			return false
		}
		seen[capability] = struct{}{}
	}
	for _, required := range requiredBridgeCapabilities {
		if _, exists := seen[required]; !exists {
			return false
		}
	}
	_, hasAllowlist := seen["ego_browser_file_allowlist_v1"]
	_, hasLearning := seen["ego_browser_site_learning_v1"]
	if binding.AllowlistRootsDigest != nil && !validSHA256(*binding.AllowlistRootsDigest) {
		return false
	}
	if binding.LearningBundleDigest != nil && !validSHA256(*binding.LearningBundleDigest) {
		return false
	}
	return hasAllowlist == (binding.AllowlistRootsDigest != nil) &&
		hasLearning == (binding.LearningBundleDigest != nil)
}

func validSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validOpaqueText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func dedicatedTaskSpace(toolSessionID string) (string, error) {
	value := "agent-remote:" + toolSessionID
	if len(value) > 256 {
		return "", fmt.Errorf("%w: dedicated task space identity", ErrProtocol)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-_:", character) {
			continue
		}
		return "", fmt.Errorf("%w: dedicated task space identity", ErrProtocol)
	}
	return value, nil
}

func validCanonicalBase64URL(value string, decodedLength int) bool {
	if value == "" || decodedLength < 0 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == decodedLength && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func randomToken(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func preparePrivateRoot(path string) error {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) || strings.Contains(path, "..") {
		return fmt.Errorf("%w: state root", ErrProtocol)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: state root permissions", ErrProtocol)
	}
	if runtime.GOOS == "linux" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || uint32(os.Geteuid()) != stat.Uid {
			return fmt.Errorf("%w: state root ownership", ErrProtocol)
		}
	}
	return os.Chmod(path, 0o700)
}

func prepareSocket(path string) error {
	if path == "" || !filepath.IsAbs(path) || strings.Contains(path, "..") {
		return fmt.Errorf("%w: socket path", ErrProtocol)
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: existing socket path", ErrProtocol)
	}
	return os.Remove(path)
}

func loadSequence(root string) (uint64, error) {
	if root == "" {
		return 0, nil
	}
	path := filepath.Join(root, "sequence.json")
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
			return 0, fmt.Errorf("%w: sequence file permissions", ErrProtocol)
		}
		if runtime.GOOS == "linux" {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || uint32(os.Geteuid()) != stat.Uid {
				return 0, fmt.Errorf("%w: sequence file ownership", ErrProtocol)
			}
		}
	} else if !os.IsNotExist(statErr) {
		return 0, statErr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var state sequenceState
	if err := decodeStrictJSON(data, &state); err != nil || state.Version != 1 {
		return 0, fmt.Errorf("%w: sequence state", ErrProtocol)
	}
	return state.NextSequence, nil
}

func persistSequence(root string, sequence uint64) error {
	if root == "" {
		return nil
	}
	data, err := json.Marshal(sequenceState{Version: 1, NextSequence: sequence})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		temporary, createErr := os.CreateTemp(root, ".sequence-*.tmp")
		if createErr != nil {
			return createErr
		}
		temporaryPath := temporary.Name()
		removeTemporary := true
		defer func() {
			if removeTemporary {
				_ = os.Remove(temporaryPath)
			}
		}()
		if err := temporary.Chmod(0o600); err != nil {
			_ = temporary.Close()
			return err
		}
		if _, err := temporary.Write(data); err != nil {
			_ = temporary.Close()
			return err
		}
		if err := temporary.Sync(); err != nil {
			_ = temporary.Close()
			return err
		}
		if err := temporary.Close(); err != nil {
			return err
		}
		if err := os.Rename(temporaryPath, filepath.Join(root, "sequence.json")); err != nil {
			if os.IsExist(err) && attempt < 2 {
				continue
			}
			return err
		}
		removeTemporary = false
		return nil
	}
	return fmt.Errorf("%w: sequence persistence collision", ErrProtocol)
}
