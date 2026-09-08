package egobrowser

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/net/websocket"
)

func testBinding(t *testing.T, generation uint64) api.EgoBrowserBinding {
	t.Helper()
	leaseUntil := time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano)
	ttlUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	return api.EgoBrowserBinding{
		BindingID:                  "binding-test",
		EgoBrowserDeviceID:         "device-test",
		EncryptionPublicKey:        base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		ToolSessionID:              "session-test",
		NodeID:                     "node-test",
		Status:                     "active",
		ControlChannel:             "ego_browser_bridge",
		RelayBindingKind:           "ego_browser",
		AuthorizationMode:          "ego_browser_script_full_trust",
		AuthorizationPolicyVersion: 1,
		Generation:                 generation,
		ReleaseProfile:             "community-local-trust",
		SignerCertificateSHA256:    "development",
		CredentialProfile:          "community_file",
		RemotePlatform:             "linux",
		LocalPlatform:              "macos",
		BridgeProtocolVersion:      ProtocolVersion,
		AllowlistRevision:          1,
		ConcurrencyMode:            "binding",
		MaxParallelRequests:        1,
		Capabilities: []string{
			"ego_browser_script_execute_v1",
			"ego_browser_snapshot_v1",
			"ego_browser_screenshot_artifact_v1",
			"ego_browser_task_space_v1",
			"ego_browser_concurrency_v1",
		},
		LeaseUntil:       &leaseUntil,
		LeaseHealth:      "healthy",
		AbsoluteTTLUntil: ttlUntil,
	}
}

func TestSetBindingsRejectsInvalidEncryptionPublicKey(t *testing.T) {
	binding := testBinding(t, 1)
	binding.EncryptionPublicKey = ""
	broker := testBrokerWithoutBindings(t)
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("accepted missing encryption key: %v", err)
	}

	binding.EncryptionPublicKey = base64.RawURLEncoding.EncodeToString(make([]byte, 31))
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("accepted short encryption key: %v", err)
	}

	binding.EncryptionPublicKey = base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 32))
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("accepted noncanonical encryption key: %v", err)
	}
}

func TestSetBindingsRejectsInvalidContractAndPolicyCapabilities(t *testing.T) {
	broker := testBrokerWithoutBindings(t)
	binding := testBinding(t, 1)
	binding.ControlChannel = "device_control"
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("accepted wrong browser channel: %v", err)
	}

	binding = testBinding(t, 1)
	binding.Capabilities = append(binding.Capabilities, "ego_browser_file_allowlist_v1")
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("accepted allowlist capability without digest: %v", err)
	}

	digest := "sha256:" + strings.Repeat("a", 64)
	binding.AllowlistRootsDigest = &digest
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); err != nil {
		t.Fatalf("rejected matching allowlist capability and digest: %v", err)
	}

	binding = testBinding(t, 1)
	binding.Capabilities = append(binding.Capabilities, binding.Capabilities[0])
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("accepted duplicate capability: %v", err)
	}
}

func testBroker(t *testing.T, binding api.EgoBrowserBinding) *Broker {
	t.Helper()
	broker := testBrokerWithoutBindings(t)
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); err != nil {
		t.Fatal(err)
	}
	return broker
}

func testBrokerWithoutBindings(t *testing.T) *Broker {
	t.Helper()
	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	socketRoot, err := os.MkdirTemp("/tmp", "ar-ego-broker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	broker, err := New(Config{
		Enabled: true, NodeID: "node-test",
		StateRoot: stateRoot, SocketPath: socketRoot + "/broker.sock",
		ControlPlaneConfigured: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	return broker
}

func registerTestSession(t *testing.T, broker *Broker, toolSessionID string) string {
	t.Helper()
	nonce, _, err := broker.RegisterToolSession(toolSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return nonce
}

func testRelayBroker(
	t *testing.T,
	ticketTTL time.Duration,
	handler func(*websocket.Conn),
) (*Broker, *atomic.Int32) {
	t.Helper()
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding(t, 1)
	binding.EncryptionPublicKey = base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	binding.MaxParallelRequests = 4

	var ticketRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node-api/ego-browser/bindings/binding-test/relay-ticket", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ticketRequests.Add(1)
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write(mustJSON(map[string]any{
			"data": map[string]any{
				"role":               "wrapper",
				"generation":         1,
				"relay_binding_kind": "ego_browser",
				"relay_path":         "/api/v1/ego-browser/bindings/binding-test/relay",
				"relay_ticket":       "ticket-test",
				"expires_at":         time.Now().UTC().Add(ticketTTL).Format(time.RFC3339Nano),
			},
		}))
	})
	mux.Handle("/api/v1/ego-browser/bindings/binding-test/relay", websocket.Handler(handler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	socketRoot, err := os.MkdirTemp("/tmp", "ar-ego-relay-broker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	broker, err := New(Config{
		Enabled:                true,
		NodeID:                 "node-test",
		StateRoot:              stateRoot,
		SocketPath:             socketRoot + "/broker.sock",
		ControlPlaneConfigured: true,
		Client:                 api.NewClient(server.URL, "node-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.SetBindings([]api.EgoBrowserBinding{binding}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	return broker, &ticketRequests
}

func testRelayRequest(requestID string, sequence uint64) ([]byte, Permit) {
	request := outerEnvelope{
		Protocol: ProtocolVersion, Channel: "ego_browser_bridge", RelayBindingKind: "ego_browser",
		Type: "execute", RequestID: requestID, BindingID: "binding-test", Generation: 1,
		Sequence: sequence, Direction: "request", PayloadBytes: 1,
		Nonce:      base64.RawURLEncoding.EncodeToString(make([]byte, 12)),
		Ciphertext: base64.RawURLEncoding.EncodeToString([]byte{byte(sequence)}),
		AuthTag:    base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
	}
	permit := Permit{SessionKey: [32]byte{byte(sequence)}}
	return mustJSON(request), permit
}

func testRelayRequestForPermit(permit Permit) []byte {
	request := outerEnvelope{
		Protocol: ProtocolVersion, Channel: "ego_browser_bridge", RelayBindingKind: "ego_browser",
		Type: "execute", RequestID: permit.RequestID, BindingID: permit.BindingID,
		Generation: permit.Generation, Sequence: permit.Sequence, Direction: "request", PayloadBytes: 1,
		Nonce:      base64.RawURLEncoding.EncodeToString(make([]byte, 12)),
		Ciphertext: base64.RawURLEncoding.EncodeToString([]byte{byte(permit.Sequence)}),
		AuthTag:    base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
	}
	return mustJSON(request)
}

func testRelayResponse(request outerEnvelope) []byte {
	request.Type = "execute_result"
	request.Direction = "response"
	request.KeyWrap = ""
	return mustJSON(request)
}

func receiveRelayRequest(connection *websocket.Conn) (outerEnvelope, error) {
	var frame []byte
	if err := binaryRelayMessage.Receive(connection, &frame); err != nil {
		return outerEnvelope{}, err
	}
	var request outerEnvelope
	if err := decodeStrictJSON(frame, &request); err != nil {
		return outerEnvelope{}, err
	}
	return request, nil
}

func decryptCancelRequest(t *testing.T, envelope outerEnvelope, sessionKey [32]byte) innerCancelRequest {
	t.Helper()
	if err := validateCancelOuter(envelope, len(mustJSON(envelope))); err != nil {
		t.Fatalf("validate cancel frame: %v", err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := base64.RawURLEncoding.DecodeString(envelope.AuthTag)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := outerAAD(envelope)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := chacha20poly1305.New(sessionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := aead.Open(nil, nonce, append(ciphertext, tag...), aad)
	if err != nil {
		t.Fatalf("authenticate cancel payload: %v", err)
	}
	var cancel innerCancelRequest
	if err := decodeStrictJSON(plaintext, &cancel); err != nil {
		t.Fatalf("decode cancel payload: %v", err)
	}
	expected := fmt.Sprintf(
		`{"protocol":%q,"request_id":%q,"sequence":%d,"type":"cancel"}`,
		InnerProtocolVersion, envelope.RequestID, envelope.Sequence,
	)
	if string(plaintext) != expected {
		t.Fatalf("cancel payload is not canonical: %s", plaintext)
	}
	return cancel
}

type forwardResult struct {
	sequence uint64
	frame    []byte
	err      error
}

func forwardForTest(
	ctx context.Context,
	broker *Broker,
	requestID string,
	sequence uint64,
	results chan<- forwardResult,
) {
	frame, permit := testRelayRequest(requestID, sequence)
	response, err := broker.forward(ctx, "binding-test", frame, permit)
	results <- forwardResult{sequence: sequence, frame: response, err: err}
}

func TestRenewalFailurePreservesOriginalGraceDeadline(t *testing.T) {
	broker := testBroker(t, testBinding(t, 1))
	state := broker.bindings["binding-test"]
	broker.markRenewalFailureFor(state)
	first := state.metadata.LeaseGraceUntil
	if first == nil || *first == "" {
		t.Fatal("expected a grace deadline")
	}
	broker.markRenewalFailureFor(state)
	if state.metadata.LeaseGraceUntil == nil || *state.metadata.LeaseGraceUntil != *first {
		t.Fatalf("renewal failure extended grace deadline: first=%v current=%v", first, state.metadata.LeaseGraceUntil)
	}
}

func TestRenewDueExpiresAfterGraceAndRejectsAdmission(t *testing.T) {
	binding := testBinding(t, 1)
	grace := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	binding.LeaseHealth = "renewal_grace"
	binding.LeaseGraceUntil = &grace
	broker := testBroker(t, binding)
	if err := broker.RenewDue(nil); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected lease expiry, got %v", err)
	}
	state := broker.bindings["binding-test"]
	if state.metadata.Status != "expired" || state.metadata.LeaseHealth != "expired" {
		t.Fatalf("binding was not terminal: %#v", state.metadata)
	}
	_, err := broker.IssuePermit(nil, PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: registerTestSession(t, broker, "session-test"),
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	})
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired binding admitted a permit: %v", err)
	}
}

func TestSetBindingsDoesNotReviveTerminalGeneration(t *testing.T) {
	binding := testBinding(t, 7)
	broker := testBroker(t, binding)
	broker.RevokeBinding(binding.BindingID, binding.Generation)
	fresh := testBinding(t, binding.Generation)
	if err := broker.SetBindings([]api.EgoBrowserBinding{fresh}); err != nil {
		t.Fatal(err)
	}
	state := broker.bindings[binding.BindingID]
	if state.metadata.Status != "revoked" || state.metadata.LeaseHealth != "expired" {
		t.Fatalf("stale snapshot revived terminal state: %#v", state.metadata)
	}
	newGeneration := testBinding(t, binding.Generation+1)
	if err := broker.SetBindings([]api.EgoBrowserBinding{newGeneration}); err != nil {
		t.Fatal(err)
	}
	if got := broker.bindings[binding.BindingID].metadata.Generation; got != binding.Generation+1 {
		t.Fatalf("new generation was not accepted: %d", got)
	}
}

func TestRelayResponseIdentityAndDirectionValidation(t *testing.T) {
	request := outerEnvelope{
		Protocol: ProtocolVersion, Channel: "ego_browser_bridge", RelayBindingKind: "ego_browser",
		Type: "execute", RequestID: "request-1", BindingID: "binding-1", Generation: 2,
		Sequence: 3, Direction: "request", PayloadBytes: 1,
	}
	response := request
	response.Type = "execute_result"
	response.Direction = "response"
	response.Nonce = base64.RawURLEncoding.EncodeToString(make([]byte, 12))
	response.AuthTag = base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	response.Ciphertext = base64.RawURLEncoding.EncodeToString([]byte{1})
	if err := validateRelayResponse(response, request); err != nil {
		t.Fatal(err)
	}
	response.Direction = "request"
	if err := validateRelayResponse(response, request); err == nil {
		t.Fatal("accepted a request-direction relay response")
	}
}

func TestPermitAdmissionEnforcesEffectiveBindingParallelism(t *testing.T) {
	binding := testBinding(t, 1)
	binding.MaxParallelRequests = 2
	broker := testBroker(t, binding)
	request := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: registerTestSession(t, broker, "session-test"),
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	}
	first, err := broker.IssuePermit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.IssuePermit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.IssuePermit(context.Background(), request); !errors.Is(err, ErrConcurrency) {
		t.Fatalf("expected concurrency rejection, got %v", err)
	}
	broker.releaseRequest(first.Sequence)
	if _, err := broker.IssuePermit(context.Background(), request); err != nil {
		t.Fatalf("permit slot was not released: %v", err)
	}
}

func TestSessionCapabilityCreatedBeforeClaimResolvesAfterRefresh(t *testing.T) {
	binding := testBinding(t, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/node-api/ego-browser/bindings", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write(mustJSON(map[string]any{
			"data": map[string]any{"items": []api.EgoBrowserBinding{binding}},
		}))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	broker, err := New(Config{
		Enabled: true, NodeID: "node-test", StateRoot: stateRoot,
		SocketPath: t.TempDir() + "/broker.sock", ControlPlaneConfigured: true,
		Client: api.NewClient(server.URL, "node-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	nonce := registerTestSession(t, broker, binding.ToolSessionID)
	request := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: nonce,
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	}
	if _, err := broker.IssuePermit(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("session without a claimed binding was admitted: %v", err)
	}
	if err := broker.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	permit, err := broker.IssuePermit(context.Background(), request)
	if err != nil {
		t.Fatalf("pre-existing session capability did not see refreshed claim: %v", err)
	}
	defer broker.releaseRequest(permit.Sequence)
	if permit.BindingID != binding.BindingID {
		t.Fatalf("resolved binding %q, want %q", permit.BindingID, binding.BindingID)
	}
}

func TestSessionCapabilitiesResolveExactBindingsAndRejectAmbiguity(t *testing.T) {
	first := testBinding(t, 1)
	second := testBinding(t, 1)
	second.BindingID = "binding-other-user"
	second.EgoBrowserDeviceID = "device-other-user"
	second.ToolSessionID = "session-other-user"
	broker := testBrokerWithoutBindings(t)
	if err := broker.SetBindings([]api.EgoBrowserBinding{first, second}); err != nil {
		t.Fatal(err)
	}
	firstNonce := registerTestSession(t, broker, first.ToolSessionID)
	secondNonce := registerTestSession(t, broker, second.ToolSessionID)
	request := func(nonce string) PermitRequest {
		return PermitRequest{
			Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: nonce,
			ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
		}
	}
	firstPermit, err := broker.IssuePermit(context.Background(), request(firstNonce))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.releaseRequest(firstPermit.Sequence)
	secondPermit, err := broker.IssuePermit(context.Background(), request(secondNonce))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.releaseRequest(secondPermit.Sequence)
	if firstPermit.BindingID != first.BindingID || secondPermit.BindingID != second.BindingID {
		t.Fatalf("session capabilities crossed bindings: first=%q second=%q", firstPermit.BindingID, secondPermit.BindingID)
	}
	if firstPermit.DefaultTaskSpace != "agent-remote:"+first.ToolSessionID ||
		secondPermit.DefaultTaskSpace != "agent-remote:"+second.ToolSessionID {
		t.Fatalf(
			"permits did not carry nonce-derived task spaces: first=%q second=%q",
			firstPermit.DefaultTaskSpace,
			secondPermit.DefaultTaskSpace,
		)
	}

	ambiguous := second
	ambiguous.BindingID = "binding-ambiguous"
	ambiguous.EgoBrowserDeviceID = "device-ambiguous"
	ambiguous.ToolSessionID = first.ToolSessionID
	if err := broker.SetBindings([]api.EgoBrowserBinding{first, ambiguous}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.IssuePermit(context.Background(), request(firstNonce)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ambiguous tool session binding was admitted: %v", err)
	}
}

func TestIssuePermitRejectsNonDedicatedTaskSpaceScope(t *testing.T) {
	binding := testBinding(t, 1)
	binding.ConcurrencyMode = "task_space"
	broker := testBroker(t, binding)
	nonce := registerTestSession(t, broker, binding.ToolSessionID)
	request := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: nonce,
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
		ConcurrencyMode: "task_space", TaskSpaceScope: "user-owned-space",
	}
	if _, err := broker.IssuePermit(context.Background(), request); !errors.Is(err, ErrProtocol) {
		t.Fatalf("non-dedicated task space scope was admitted: %v", err)
	}

	request.TaskSpaceScope = "agent-remote:" + binding.ToolSessionID
	permit, err := broker.IssuePermit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.releaseRequest(permit.Sequence)
	if permit.DefaultTaskSpace != request.TaskSpaceScope || permit.TaskSpaceScope != request.TaskSpaceScope {
		t.Fatalf(
			"permit did not use the nonce-derived task space: default=%q scope=%q",
			permit.DefaultTaskSpace,
			permit.TaskSpaceScope,
		)
	}
	response := permitResponse(broker, permit)
	if response.Permit.DefaultTaskSpace != request.TaskSpaceScope {
		t.Fatalf("wire permit omitted the dedicated task space: %q", response.Permit.DefaultTaskSpace)
	}
}

func TestRegisterToolSessionRejectsInvalidDedicatedTaskSpaceName(t *testing.T) {
	broker := testBrokerWithoutBindings(t)
	if _, _, err := broker.RegisterToolSession("session with spaces"); !errors.Is(err, ErrProtocol) {
		t.Fatalf("invalid dedicated task space name was registered: %v", err)
	}
}

func TestUnregisterToolSessionRevokesCapabilityAndOutstandingPermit(t *testing.T) {
	broker := testBroker(t, testBinding(t, 1))
	nonce, created, err := broker.RegisterToolSession("session-test")
	if err != nil || !created {
		t.Fatalf("register session: created=%v err=%v", created, err)
	}
	same, created, err := broker.RegisterToolSession("session-test")
	if err != nil || created || same != nonce {
		t.Fatalf("session registration was not idempotent: nonce=%q created=%v err=%v", same, created, err)
	}
	request := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: nonce,
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	}
	permit, err := broker.IssuePermit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if broker.UnregisterToolSession("session-test", "wrong-nonce") {
		t.Fatal("mismatched rollback nonce removed the session")
	}
	if !broker.UnregisterToolSession("session-test", nonce) {
		t.Fatal("session capability was not removed")
	}
	if _, err := broker.ConsumePermit(permit.BindingID, permit.Generation, permit.RequestID, permit.Sequence, 1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("outstanding permit survived session stop: %v", err)
	}
	if _, err := broker.IssuePermit(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("removed session nonce was accepted: %v", err)
	}
}

func TestToolSessionPeerRequiresTrustedRuntimeUIDAndExactNonce(t *testing.T) {
	broker := testBrokerWithoutBindings(t)
	runtimeUID := uint32(os.Getuid())
	var grantedPath string
	var grantedUID uint32
	var grants atomic.Int32
	broker.cfg.GrantPeerAccess = func(socketPath string, uid uint32) error {
		grantedPath = socketPath
		grantedUID = uid
		grants.Add(1)
		return nil
	}
	nonce := registerTestSession(t, broker, "session-test")
	if err := broker.authorizePeerUID(nonce, runtimeUID); !errors.Is(err, ErrProtocol) {
		t.Fatalf("unbound runtime UID was accepted: %v", err)
	}
	if err := broker.AuthorizeToolSessionPeer("session-test", nonce, runtimeUID); err != nil {
		t.Fatal(err)
	}
	if grantedPath != broker.SocketPath() || grantedUID != runtimeUID || grants.Load() != 1 {
		t.Fatalf("unexpected peer access grant: path=%q uid=%d grants=%d", grantedPath, grantedUID, grants.Load())
	}
	if err := broker.authorizePeerUID(nonce, runtimeUID); err != nil {
		t.Fatalf("trusted runtime UID and nonce were rejected: %v", err)
	}
	if err := broker.authorizePeerUID(nonce, runtimeUID+1); !errors.Is(err, ErrProtocol) {
		t.Fatalf("wrong runtime UID was accepted: %v", err)
	}
	if err := broker.authorizePeerUID("wrong-nonce", runtimeUID); !errors.Is(err, ErrProtocol) {
		t.Fatalf("wrong session nonce was accepted: %v", err)
	}
	if err := broker.AuthorizeToolSessionPeer("session-test", nonce, runtimeUID+1); !errors.Is(err, ErrProtocol) {
		t.Fatalf("runtime UID changed after authorization: %v", err)
	}
	if err := broker.AuthorizeToolSessionPeer("session-test", nonce, runtimeUID); err != nil || grants.Load() != 1 {
		t.Fatalf("idempotent UID authorization failed: err=%v grants=%d", err, grants.Load())
	}

	handled := make(chan error, 1)
	go func() {
		connection, err := broker.listener.AcceptUnix()
		if err != nil {
			handled <- err
			return
		}
		handled <- broker.handleConnection(connection)
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: broker.SocketPath(), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	request := mustJSON(map[string]any{
		"protocol": ProtocolVersion, "type": "doctor", "startup_nonce": nonce,
		"payload": map[string]any{},
	})
	if err := writeFrame(client, request); err != nil {
		t.Fatal(err)
	}
	response, err := readFrame(client)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if !bytes.Contains(response, []byte(`"type":"doctor_response"`)) {
		t.Fatalf("unexpected broker response: %s", response)
	}
	if err := <-handled; err != nil {
		t.Fatal(err)
	}
	if !broker.UnregisterToolSession("session-test", nonce) {
		t.Fatal("failed to unregister authorized session")
	}
	if err := broker.authorizePeerUID(nonce, runtimeUID); !errors.Is(err, ErrProtocol) {
		t.Fatalf("unregistered runtime UID and nonce were accepted: %v", err)
	}
}

func TestToolSessionIdentityDriftClearsOutstandingMaterials(t *testing.T) {
	binding := testBinding(t, 1)
	broker := testBroker(t, binding)
	nonce := registerTestSession(t, broker, binding.ToolSessionID)
	permit, err := broker.IssuePermit(context.Background(), PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: nonce,
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := binding
	changed.ToolSessionID = "session-reassigned"
	if err := broker.SetBindings([]api.EgoBrowserBinding{changed}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.ConsumePermit(
		permit.BindingID,
		permit.Generation,
		permit.RequestID,
		permit.Sequence,
		1,
	); !errors.Is(err, ErrRevoked) {
		t.Fatalf("tool-session drift retained an outstanding permit: %v", err)
	}
}

func TestBrokerRestartInvalidatesSessionCapabilities(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Enabled: true, NodeID: "node-test", StateRoot: stateRoot,
		SocketPath: t.TempDir() + "/broker.sock", ControlPlaneConfigured: false,
	}
	first, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetBindings([]api.EgoBrowserBinding{testBinding(t, 1)}); err != nil {
		t.Fatal(err)
	}
	oldNonce := registerTestSession(t, first, "session-test")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.SetBindings([]api.EgoBrowserBinding{testBinding(t, 1)}); err != nil {
		t.Fatal(err)
	}
	request := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: oldNonce,
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	}
	if _, err := second.IssuePermit(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pre-restart session capability was accepted: %v", err)
	}
	newNonce := registerTestSession(t, second, "session-test")
	if newNonce == oldNonce {
		t.Fatal("broker restart reused a session capability")
	}
}

func TestPermitRequestRejectsWrapperSuppliedBindingIdentity(t *testing.T) {
	frame := []byte(`{"protocol":"ego-browser-bridge-v1","type":"permit_request","startup_nonce":"nonce","binding_id":"binding-test","script_bytes":1,"timeout_ms":1,"cwd_label":"workspace","concurrency_mode":"binding"}`)
	var request PermitRequest
	if err := decodeStrictJSON(frame, &request); err == nil {
		t.Fatal("permit request accepted a wrapper-supplied binding identity")
	}
}

func TestRelayDispatchesOutOfOrderResponsesAndKeepsEstablishedConnectionAfterTicketExpiry(t *testing.T) {
	handlerDone := make(chan struct{})
	broker, ticketRequests := testRelayBroker(t, 200*time.Millisecond, func(connection *websocket.Conn) {
		defer close(handlerDone)
		first, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		second, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
		if err := binaryRelayMessage.Send(connection, testRelayResponse(second)); err != nil {
			return
		}
		if err := binaryRelayMessage.Send(connection, testRelayResponse(first)); err != nil {
			return
		}
		third, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		_ = binaryRelayMessage.Send(connection, testRelayResponse(third))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan forwardResult, 2)
	go forwardForTest(ctx, broker, "request-1", 1, results)
	go forwardForTest(ctx, broker, "request-2", 2, results)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("forward %d failed: %v", result.sequence, result.err)
		}
		var response outerEnvelope
		if err := decodeStrictJSON(result.frame, &response); err != nil {
			t.Fatal(err)
		}
		if response.Sequence != result.sequence {
			t.Fatalf("response %d was dispatched to request %d", response.Sequence, result.sequence)
		}
	}
	third := make(chan forwardResult, 1)
	go forwardForTest(ctx, broker, "request-3", 3, third)
	result := <-third
	if result.err != nil {
		t.Fatalf("established relay was rejected after ticket expiry: %v", result.err)
	}
	if count := ticketRequests.Load(); count != 1 {
		t.Fatalf("established relay unexpectedly requested %d tickets", count)
	}
	select {
	case <-handlerDone:
	case <-ctx.Done():
		t.Fatal("relay test handler did not finish")
	}
}

func TestRelaySendsConsumedPermitsInMonotonicSequenceOrder(t *testing.T) {
	received := make(chan uint64, 2)
	broker, _ := testRelayBroker(t, time.Minute, func(connection *websocket.Conn) {
		for range 2 {
			request, err := receiveRelayRequest(connection)
			if err != nil {
				return
			}
			received <- request.Sequence
			if binaryRelayMessage.Send(connection, testRelayResponse(request)) != nil {
				return
			}
		}
	})
	permitRequest := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: registerTestSession(t, broker, "session-test"),
		ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace",
	}
	first, err := broker.IssuePermit(context.Background(), permitRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.releaseRequest(first.Sequence)
	second, err := broker.IssuePermit(context.Background(), permitRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.releaseRequest(second.Sequence)
	firstFrame := testRelayRequestForPermit(first)
	secondFrame := testRelayRequestForPermit(second)
	first, err = broker.ConsumePermit(first.BindingID, first.Generation, first.RequestID, first.Sequence, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err = broker.ConsumePermit(second.BindingID, second.Generation, second.RequestID, second.Sequence, 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan forwardResult, 2)
	go func() {
		response, forwardErr := broker.forward(ctx, second.BindingID, secondFrame, second)
		results <- forwardResult{sequence: second.Sequence, frame: response, err: forwardErr}
	}()
	select {
	case sequence := <-received:
		t.Fatalf("higher sequence %d was sent before the lower request", sequence)
	case <-time.After(50 * time.Millisecond):
	}
	go func() {
		response, forwardErr := broker.forward(ctx, first.BindingID, firstFrame, first)
		results <- forwardResult{sequence: first.Sequence, frame: response, err: forwardErr}
	}()
	if sequence := <-received; sequence != first.Sequence {
		t.Fatalf("first wire sequence was %d, want %d", sequence, first.Sequence)
	}
	if sequence := <-received; sequence != second.Sequence {
		t.Fatalf("second wire sequence was %d, want %d", sequence, second.Sequence)
	}
	for range 2 {
		if result := <-results; result.err != nil {
			t.Fatalf("ordered relay forward failed: %v", result.err)
		}
	}
}

func TestRelayDiscardsCancelledResponseWithoutCorruptingNextRequest(t *testing.T) {
	firstReceived := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstSent := make(chan struct{})
	cancelFrame := make(chan outerEnvelope, 1)
	broker, ticketRequests := testRelayBroker(t, time.Minute, func(connection *websocket.Conn) {
		first, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		close(firstReceived)
		cancel, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		cancelFrame <- cancel
		<-releaseFirst
		if binaryRelayMessage.Send(connection, testRelayResponse(first)) != nil {
			return
		}
		close(firstSent)
		second, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		_ = binaryRelayMessage.Send(connection, testRelayResponse(second))
	})
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan forwardResult, 1)
	go forwardForTest(firstContext, broker, "request-cancelled", 1, firstResult)
	<-firstReceived
	cancelFirst()
	if result := <-firstResult; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", result.err)
	}
	cancelEnvelope := <-cancelFrame
	cancel := decryptCancelRequest(t, cancelEnvelope, [32]byte{1})
	if cancel.RequestID != "request-cancelled" || cancel.Sequence != 1 {
		t.Fatalf("cancel targeted the wrong request: %#v", cancel)
	}
	close(releaseFirst)
	<-firstSent

	secondContext, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	secondResult := make(chan forwardResult, 1)
	go forwardForTest(secondContext, broker, "request-after-cancel", 2, secondResult)
	result := <-secondResult
	if result.err != nil {
		t.Fatalf("response after abandoned request failed: %v", result.err)
	}
	if count := ticketRequests.Load(); count != 1 {
		t.Fatalf("cancellation replaced a healthy relay connection: %d tickets", count)
	}
}

func TestWrapperSocketEOFCancelsRequestAndKeepsRelayUsable(t *testing.T) {
	firstReceived := make(chan outerEnvelope, 1)
	cancelReceived := make(chan outerEnvelope, 1)
	releaseLateResponse := make(chan struct{})
	lateResponseSent := make(chan struct{})
	handlerDone := make(chan struct{})
	broker, ticketRequests := testRelayBroker(t, time.Minute, func(connection *websocket.Conn) {
		defer close(handlerDone)
		first, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		firstReceived <- first
		cancel, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		cancelReceived <- cancel
		<-releaseLateResponse
		if binaryRelayMessage.Send(connection, testRelayResponse(first)) != nil {
			return
		}
		close(lateResponseSent)
		second, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		_ = binaryRelayMessage.Send(connection, testRelayResponse(second))
	})

	nonce := registerTestSession(t, broker, "session-test")
	if err := broker.AuthorizeToolSessionPeer("session-test", nonce, uint32(os.Getuid())); err != nil {
		t.Fatal(err)
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- broker.Serve(serveContext) }()

	openWrapperRequest := func() (*net.UnixConn, Permit) {
		connection, err := net.DialUnix(
			"unix",
			nil,
			&net.UnixAddr{Name: broker.SocketPath(), Net: "unix"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		request := PermitRequest{
			Protocol: ProtocolVersion, Type: "permit_request", StartupNonce: nonce,
			ScriptBytes: 1, TimeoutMS: 1, CWDLabel: "workspace", ConcurrencyMode: "binding",
		}
		if err := writeFrame(connection, mustJSON(request)); err != nil {
			t.Fatal(err)
		}
		frame, err := readFrame(connection)
		if err != nil {
			t.Fatal(err)
		}
		var response permitResponseWire
		if err := decodeStrictJSON(frame, &response); err != nil {
			t.Fatal(err)
		}
		keyBytes, err := base64.RawURLEncoding.DecodeString(response.SessionKey)
		if err != nil || len(keyBytes) != 32 {
			t.Fatalf("invalid permit session key: bytes=%d err=%v", len(keyBytes), err)
		}
		permit := Permit{
			BindingID: response.Permit.BindingID, Generation: response.Permit.Generation,
			RequestID: response.Permit.RequestID, Sequence: response.Permit.Sequence,
		}
		copy(permit.SessionKey[:], keyBytes)
		if err := writeFrame(connection, testRelayRequestForPermit(permit)); err != nil {
			t.Fatal(err)
		}
		return connection, permit
	}

	firstConnection, firstPermit := openWrapperRequest()
	select {
	case first := <-firstReceived:
		if first.RequestID != firstPermit.RequestID || first.Sequence != firstPermit.Sequence {
			t.Fatalf("relay received the wrong first request: %#v", first)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not receive the first wrapper request")
	}
	if err := firstConnection.Close(); err != nil {
		t.Fatal(err)
	}
	var cancelEnvelope outerEnvelope
	select {
	case cancelEnvelope = <-cancelReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("wrapper socket EOF did not emit a relay cancellation")
	}
	inner := decryptCancelRequest(t, cancelEnvelope, firstPermit.SessionKey)
	if cancelEnvelope.BindingID != firstPermit.BindingID ||
		cancelEnvelope.Generation != firstPermit.Generation ||
		cancelEnvelope.RequestID != firstPermit.RequestID ||
		cancelEnvelope.Sequence != firstPermit.Sequence ||
		inner.RequestID != firstPermit.RequestID || inner.Sequence != firstPermit.Sequence {
		t.Fatalf("socket EOF cancellation changed request identity: outer=%#v inner=%#v", cancelEnvelope, inner)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		broker.mu.RLock()
		_, active := broker.active[firstPermit.Sequence]
		broker.mu.RUnlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled wrapper request remained active")
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseLateResponse)
	select {
	case <-lateResponseSent:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not send the late cancelled response")
	}

	secondConnection, secondPermit := openWrapperRequest()
	responseFrame, err := readFrame(secondConnection)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondConnection.Close(); err != nil {
		t.Fatal(err)
	}
	var response outerEnvelope
	if err := decodeStrictJSON(responseFrame, &response); err != nil {
		t.Fatal(err)
	}
	if response.RequestID != secondPermit.RequestID || response.Sequence != secondPermit.Sequence {
		t.Fatalf("relay returned the wrong second response: %#v", response)
	}
	if count := ticketRequests.Load(); count != 1 {
		t.Fatalf("socket EOF replaced the healthy relay connection: %d tickets", count)
	}
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("relay handler did not finish")
	}

	cancelServe()
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("broker IPC server did not stop")
	}
}

func TestCancelRequestUsesOriginalIdentityAndKeepsRelayUsable(t *testing.T) {
	firstReceived := make(chan outerEnvelope, 1)
	cancelReceived := make(chan outerEnvelope, 1)
	releaseCancelled := make(chan struct{})
	broker, ticketRequests := testRelayBroker(t, time.Minute, func(connection *websocket.Conn) {
		first, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		firstReceived <- first
		cancel, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		cancelReceived <- cancel
		<-releaseCancelled
		if binaryRelayMessage.Send(connection, testRelayResponse(first)) != nil {
			return
		}
		second, err := receiveRelayRequest(connection)
		if err != nil {
			return
		}
		_ = binaryRelayMessage.Send(connection, testRelayResponse(second))
	})
	request := PermitRequest{
		Protocol: ProtocolVersion, Type: "permit_request",
		StartupNonce: registerTestSession(t, broker, "session-test"),
		ScriptBytes:  1, TimeoutMS: 1, CWDLabel: "workspace",
	}
	permit, err := broker.IssuePermit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.releaseRequest(permit.Sequence)
	frame := testRelayRequestForPermit(permit)
	consumed, err := broker.ConsumePermit(
		permit.BindingID, permit.Generation, permit.RequestID, permit.Sequence, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelContext := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelContext()
	firstResult := make(chan forwardResult, 1)
	go func() {
		response, forwardErr := broker.forward(ctx, permit.BindingID, frame, consumed)
		firstResult <- forwardResult{sequence: permit.Sequence, frame: response, err: forwardErr}
	}()
	first := <-firstReceived
	if first.RequestID != permit.RequestID || first.Sequence != permit.Sequence {
		t.Fatalf("first request identity changed: %#v", first)
	}
	if _, err := broker.CancelRequest(
		permit.BindingID, permit.Generation, "mismatched-request", permit.Sequence,
	); !errors.Is(err, ErrProtocol) {
		t.Fatalf("mismatched cancellation was not rejected: %v", err)
	}
	cancelled, err := broker.CancelRequest(
		permit.BindingID, permit.Generation, permit.RequestID, permit.Sequence,
	)
	if err != nil || !cancelled {
		t.Fatalf("cancel exact request: cancelled=%t err=%v", cancelled, err)
	}
	if cancelled, err = broker.CancelRequest(
		permit.BindingID, permit.Generation, permit.RequestID, permit.Sequence,
	); err != nil || !cancelled {
		t.Fatalf("repeat exact cancellation: cancelled=%t err=%v", cancelled, err)
	}
	type cancellationResult struct {
		requestActive          bool
		serverTerminalObserved bool
		err                    error
	}
	cancellationCompleted := make(chan cancellationResult, 1)
	go func() {
		active, observed, waitErr := broker.CancelRequestAndWait(
			ctx, permit.BindingID, permit.Generation, permit.RequestID, permit.Sequence,
		)
		cancellationCompleted <- cancellationResult{
			requestActive:          active,
			serverTerminalObserved: observed,
			err:                    waitErr,
		}
	}()
	cancelEnvelope := <-cancelReceived
	inner := decryptCancelRequest(t, cancelEnvelope, consumed.SessionKey)
	if cancelEnvelope.BindingID != first.BindingID || cancelEnvelope.Generation != first.Generation ||
		cancelEnvelope.RequestID != first.RequestID || cancelEnvelope.Sequence != first.Sequence ||
		inner.RequestID != first.RequestID || inner.Sequence != first.Sequence {
		t.Fatalf("cancellation did not preserve the request identity: outer=%#v inner=%#v", cancelEnvelope, inner)
	}
	select {
	case result := <-cancellationCompleted:
		t.Fatalf("cancellation completed before a terminal relay outcome: %#v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCancelled)
	if result := <-firstResult; result.err != nil {
		t.Fatalf("cancelled request did not receive its terminal response: %v", result.err)
	}
	result := <-cancellationCompleted
	if result.err != nil || !result.requestActive || !result.serverTerminalObserved {
		t.Fatalf("unexpected cancellation completion: %#v", result)
	}
	broker.releaseRequest(permit.Sequence)

	secondPermit, err := broker.IssuePermit(context.Background(), request)
	if err != nil {
		t.Fatalf("binding rejected work after request cancellation: %v", err)
	}
	defer broker.releaseRequest(secondPermit.Sequence)
	secondFrame := testRelayRequestForPermit(secondPermit)
	secondPermit, err = broker.ConsumePermit(
		secondPermit.BindingID, secondPermit.Generation, secondPermit.RequestID, secondPermit.Sequence, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.forward(ctx, secondPermit.BindingID, secondFrame, secondPermit); err != nil {
		t.Fatalf("subsequent request failed: %v", err)
	}
	if count := ticketRequests.Load(); count != 1 {
		t.Fatalf("request cancellation replaced the healthy relay: %d tickets", count)
	}
}

func TestRelayRevocationFailsAllPendingRequests(t *testing.T) {
	bothReceived := make(chan struct{})
	releaseHandler := make(chan struct{})
	broker, _ := testRelayBroker(t, time.Minute, func(connection *websocket.Conn) {
		if _, err := receiveRelayRequest(connection); err != nil {
			return
		}
		if _, err := receiveRelayRequest(connection); err != nil {
			return
		}
		close(bothReceived)
		<-releaseHandler
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan forwardResult, 2)
	go forwardForTest(ctx, broker, "request-revoked-1", 1, results)
	go forwardForTest(ctx, broker, "request-revoked-2", 2, results)
	<-bothReceived
	broker.RevokeBinding("binding-test", 1)
	for range 2 {
		result := <-results
		if !errors.Is(result.err, ErrRevoked) {
			t.Fatalf("pending request did not receive revocation: %v", result.err)
		}
	}
	close(releaseHandler)
}

func TestExecuteMetricsUseOnlyContentFreeDimensions(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	recordExecuteMetrics(time.Now().Add(-10*time.Millisecond), "completed", 123, 456)
	logged := output.String()
	for _, expected := range []string{
		"metric=ego_browser_execute_total value=1 unit=executions status=completed",
		"metric=ego_browser_execute_duration_seconds",
		"metric=ego_browser_bytes_total value=123 unit=bytes direction=request status=completed",
		"metric=ego_browser_bytes_total value=456 unit=bytes direction=response status=completed",
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("metric output missing %q: %s", expected, logged)
		}
	}
	for _, forbidden := range []string{"binding-test", "session-test", "request-test", "heredoc"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("metric output contains forbidden content %q", forbidden)
		}
	}

	statuses := map[error]string{
		ErrConcurrency:           "concurrency_conflict",
		ErrLeaseRenewalRequired:  "renewal_required",
		ErrLeaseExpired:          "lease_expired",
		ErrRevoked:               "unavailable",
		context.DeadlineExceeded: "timeout",
		ErrProtocol:              "rejected",
	}
	for err, expected := range statuses {
		if actual := executeMetricStatus(err); actual != expected {
			t.Fatalf("status for %v = %q, want %q", err, actual, expected)
		}
	}
}
