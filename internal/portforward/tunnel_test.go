package portforward

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"golang.org/x/net/http2"
)

type fakeAPI struct {
	mu            sync.Mutex
	lease         api.PortForwardLease
	redeem        api.RedeemPortForwardRequest
	released      api.ReleasePortForwardRequest
	releaseErrors []error
	releaseCalls  int
	renewErr      error
	renewErrors   []error
	renewRequests []api.RenewPortForwardRequest
}

func (f *fakeAPI) RedeemPortForward(_ context.Context, request api.RedeemPortForwardRequest) (api.PortForwardLeaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redeem = request
	return api.PortForwardLeaseResponse{Data: f.lease}, nil
}

func (f *fakeAPI) RenewPortForward(_ context.Context, _ string, request api.RenewPortForwardRequest) (api.PortForwardLeaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewRequests = append(f.renewRequests, request)
	if len(f.renewErrors) > 0 {
		err := f.renewErrors[0]
		f.renewErrors = f.renewErrors[1:]
		return api.PortForwardLeaseResponse{Data: f.lease}, err
	}
	return api.PortForwardLeaseResponse{Data: f.lease}, f.renewErr
}

func (f *fakeAPI) ReleasePortForward(_ context.Context, _ string, request api.ReleasePortForwardRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	f.released = request
	if len(f.releaseErrors) > 0 {
		err := f.releaseErrors[0]
		f.releaseErrors = f.releaseErrors[1:]
		return err
	}
	return nil
}

func TestReleasePortForwardRetriesTransientFailures(t *testing.T) {
	client := &fakeAPI{releaseErrors: []error{errors.New("first"), errors.New("second")}}
	releasePortForward(context.Background(), client, "forward-1", api.ReleasePortForwardRequest{Generation: 3})
	if client.releaseCalls != 3 || client.released.Generation != 3 {
		t.Fatalf("release was not retried with the original generation: calls=%d request=%#v", client.releaseCalls, client.released)
	}
}

type tcpRuntimeDialer struct {
	address string
	mu      sync.Mutex
	payload runtimehelper.DialSessionLoopbackPayload
}

type runtimeDialerFunc func(context.Context, string, runtimehelper.DialSessionLoopbackPayload) (net.Conn, error)

func (f runtimeDialerFunc) DialSessionLoopback(ctx context.Context, requestID string, payload runtimehelper.DialSessionLoopbackPayload) (net.Conn, error) {
	return f(ctx, requestID, payload)
}

func (d *tcpRuntimeDialer) DialSessionLoopback(_ context.Context, _ string, payload runtimehelper.DialSessionLoopbackPayload) (net.Conn, error) {
	d.mu.Lock()
	d.payload = payload
	d.mu.Unlock()
	return net.Dial("tcp", d.address)
}

func testLease() api.PortForwardLease {
	return api.PortForwardLease{
		ForwardID: "forward-1", SessionID: "session-1", RuntimeBackend: "native",
		RuntimeResourceID: "agent-remote-session.service", RemotePort: 5173,
		Generation: 1, LeaseExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano),
		MaxStreams: 8, ControlPlaneGraceSeconds: 30,
	}
}

func TestHandshakeRejectsUnknownFieldsAndOversizedPayloads(t *testing.T) {
	unknown := handshakeBytes(t, map[string]any{
		"forward_id": "forward-1", "connect_token": "secret-token",
		"client_version": "1.0.0", "max_streams": 8, "host": "127.0.0.1",
	})
	if _, err := readClientHandshake(bytes.NewReader(unknown)); err == nil {
		t.Fatal("expected unknown target field to be rejected")
	}
	oversized := append([]byte(protocolMagic), 0, 0, 32, 1)
	if _, err := readClientHandshake(bytes.NewReader(oversized)); err == nil {
		t.Fatal("expected oversized handshake to be rejected")
	}
	duplicatePayload := []byte(`{"forward_id":"forward-1","forward_id":"forward-2","connect_token":"secret-token","client_version":"1.0.0","max_streams":8}`)
	duplicate := bytes.NewBufferString(protocolMagic)
	if err := binary.Write(duplicate, binary.BigEndian, uint32(len(duplicatePayload))); err != nil {
		t.Fatal(err)
	}
	duplicate.Write(duplicatePayload)
	if _, err := readClientHandshake(duplicate); err == nil {
		t.Fatal("expected duplicate handshake fields to be rejected")
	}
}

func TestHandshakeRejectsTruncatedAndInvalidEnvelopes(t *testing.T) {
	truncatedPayload := bytes.NewBufferString(protocolMagic)
	if err := binary.Write(truncatedPayload, binary.BigEndian, uint32(4)); err != nil {
		t.Fatal(err)
	}
	truncatedPayload.WriteString("{}")
	invalidLimits := handshakeBytes(t, clientHandshake{
		ForwardID: "forward-1", ConnectToken: strings.Repeat("x", 32),
		ClientVersion: strings.Repeat("v", 65), MaxStreams: 0,
	})
	for name, payload := range map[string][]byte{
		"missing magic":      {},
		"missing length":     []byte(protocolMagic),
		"empty payload":      append([]byte(protocolMagic), 0, 0, 0, 0),
		"truncated payload":  truncatedPayload.Bytes(),
		"non object payload": handshakeBytes(t, []string{"not", "an", "object"}),
		"invalid limits":     invalidLimits,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readClientHandshake(bytes.NewReader(payload)); err == nil {
				t.Fatal("invalid handshake was accepted")
			}
		})
	}
}

func TestServeRejectsMissingDependenciesIdentityAndInvalidHandshake(t *testing.T) {
	if err := Serve(context.Background(), Config{}); err == nil {
		t.Fatal("missing dependencies were accepted")
	}
	serverConnection, clientConnection := net.Pipe()
	client := &fakeAPI{lease: testLease()}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(context.Background(), Config{
			Client: client, Runtime: runtimeDialerFunc(func(context.Context, string, runtimehelper.DialSessionLoopbackPayload) (net.Conn, error) {
				return nil, errors.New("unused")
			}), Connection: serverConnection,
		})
	}()
	if err := <-serveDone; err == nil {
		t.Fatal("missing identity was accepted")
	}
	_ = clientConnection.Close()

	serverConnection, clientConnection = net.Pipe()
	serveDone = make(chan error, 1)
	go func() {
		serveDone <- Serve(context.Background(), Config{
			ForwardID: "forward-1", DeviceID: "device-1", SSHKeyID: "key-1",
			Client: client, Runtime: runtimeDialerFunc(func(context.Context, string, runtimehelper.DialSessionLoopbackPayload) (net.Conn, error) {
				return nil, errors.New("unused")
			}), Connection: serverConnection,
		})
	}()
	if _, err := clientConnection.Write([]byte("BADBAD")); err != nil {
		t.Fatal(err)
	}
	handshake := readServerHandshake(t, clientConnection)
	if handshake.OK || handshake.ErrorCode != "AUTH_INVALID" {
		t.Fatalf("unexpected invalid-handshake response: %#v", handshake)
	}
	_ = clientConnection.Close()
	if err := <-serveDone; err == nil {
		t.Fatal("invalid handshake did not fail Serve")
	}
}

func TestStreamRequestRejectsDuplicateIdentityAndArbitraryHeaders(t *testing.T) {
	request, err := http.NewRequest(http.MethodConnect, "https://session-loopback", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "session-loopback"
	request.Header.Add("x-agent-remote-forward-id", "forward-1")
	if !validStreamRequest(request, "forward-1") {
		t.Fatal("valid fixed-target CONNECT request was rejected")
	}
	request.Header.Add("x-agent-remote-forward-id", "forward-1")
	if validStreamRequest(request, "forward-1") {
		t.Fatal("duplicate forward identity header was accepted")
	}
	request.Header = http.Header{"X-Agent-Remote-Forward-Id": {"forward-1"}, "X-Target-Host": {"169.254.169.254"}}
	if validStreamRequest(request, "forward-1") {
		t.Fatal("arbitrary target header was accepted")
	}
}

func TestPublicErrorCodeUsesBoundedControlPlaneCodes(t *testing.T) {
	if code := publicErrorCode(&api.HTTPError{StatusCode: http.StatusConflict, Code: "TUNNEL_EXPIRED"}); code != "TUNNEL_EXPIRED" {
		t.Fatalf("unexpected typed error code %q", code)
	}
	if code := publicErrorCode(errors.New("private upstream details")); code != "CONTROL_PLANE_UNAVAILABLE" {
		t.Fatalf("unexpected fallback error code %q", code)
	}
}

func TestHandlerRejectsInvalidLimitedCancelledAndFailedStreams(t *testing.T) {
	newHandler := func(ctx context.Context) handler {
		return handler{
			ctx: ctx, forwardID: "forward-1", generation: 1,
			lease: testLease(), semaphore: make(chan struct{}, 1), dialTimeout: time.Second,
			counters: &counters{}, upload: newBandwidthLimiter(0), download: newBandwidthLimiter(0),
		}
	}
	request := httptest.NewRequest(http.MethodGet, "https://session-loopback", nil)
	response := httptest.NewRecorder()
	base := newHandler(context.Background())
	base.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected invalid request status %d", response.Code)
	}

	validRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodConnect, "https://session-loopback", nil)
		request.Host = "session-loopback"
		request.Header.Set("x-agent-remote-forward-id", "forward-1")
		return request
	}
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := newHandler(cancelledContext)
	cancelledResponse := httptest.NewRecorder()
	cancelled.ServeHTTP(cancelledResponse, validRequest())
	if cancelledResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected cancelled status %d", cancelledResponse.Code)
	}

	limited := newHandler(context.Background())
	limited.semaphore <- struct{}{}
	limitedResponse := httptest.NewRecorder()
	limited.ServeHTTP(limitedResponse, validRequest())
	if limitedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("unexpected limited status %d", limitedResponse.Code)
	}
	<-limited.semaphore

	for name, expected := range map[string]struct {
		err    error
		status int
	}{
		"runtime failure": {err: errors.New("runtime unavailable"), status: http.StatusBadGateway},
		"dial timeout":    {err: context.DeadlineExceeded, status: http.StatusGatewayTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			failed := newHandler(context.Background())
			failed.runtime = runtimeDialerFunc(func(context.Context, string, runtimehelper.DialSessionLoopbackPayload) (net.Conn, error) {
				return nil, expected.err
			})
			failedResponse := httptest.NewRecorder()
			failed.ServeHTTP(failedResponse, validRequest())
			if failedResponse.Code != expected.status {
				t.Fatalf("unexpected failed dial status %d", failedResponse.Code)
			}
		})
	}
}

func TestServeMultiplexesConnectToFixedRuntimeTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()
	serverConnection, clientConnection := net.Pipe()
	client := &fakeAPI{lease: testLease()}
	runtimeDialer := &tcpRuntimeDialer{address: listener.Addr().String()}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(context.Background(), Config{
			ForwardID: "forward-1", DeviceID: "device-1", SSHKeyID: "key-1",
			Client: client, Runtime: runtimeDialer, Connection: serverConnection,
			RenewPeriod: time.Hour,
		})
	}()
	if _, err := clientConnection.Write(handshakeBytes(t, clientHandshake{
		ForwardID: "forward-1", ConnectToken: "one-time-token-with-at-least-32-bytes",
		ClientVersion: "1.0.0", MaxStreams: 4,
	})); err != nil {
		t.Fatal(err)
	}
	handshake := readServerHandshake(t, clientConnection)
	if !handshake.OK || handshake.MaxStreams != 4 {
		t.Fatalf("unexpected server handshake: %#v", handshake)
	}
	transport := &http2.Transport{}
	http2Client, err := transport.NewClientConn(clientConnection)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodConnect, "https://session-loopback", bytes.NewReader([]byte("ping")))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "session-loopback"
	request.Header.Set("x-agent-remote-forward-id", "forward-1")
	response, err := http2Client.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(payload) != "ping" {
		t.Fatalf("unexpected tunnel response %d %q", response.StatusCode, payload)
	}
	client.mu.Lock()
	if client.redeem.ConnectToken != "one-time-token-with-at-least-32-bytes" || client.redeem.SSHKeyID != "key-1" {
		t.Fatalf("unexpected redeem request: %#v", client.redeem)
	}
	client.mu.Unlock()
	runtimeDialer.mu.Lock()
	if runtimeDialer.payload.SessionID != "session-1" || runtimeDialer.payload.Port != 5173 {
		t.Fatalf("unexpected runtime target: %#v", runtimeDialer.payload)
	}
	runtimeDialer.mu.Unlock()
	_ = http2Client.Close()
	_ = clientConnection.Close()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tunnel server did not stop")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.released.ConnectionCountTotal != 1 || client.released.BytesUpTotal != 4 || client.released.BytesDownTotal != 4 {
		t.Fatalf("unexpected release counters: %#v", client.released)
	}
}

func TestMaintainLeaseFailsClosedOnAuthorizationError(t *testing.T) {
	client := &fakeAPI{
		lease:    testLease(),
		renewErr: &api.HTTPError{StatusCode: http.StatusConflict, Code: "TUNNEL_EXPIRED"},
	}
	lease := client.lease
	current := &atomic.Pointer[api.PortForwardLease]{}
	current.Store(&lease)
	ctx, cancelContext := context.WithCancel(context.Background())
	defer cancelContext()
	cancelled := make(chan struct{})
	cancel := func() {
		select {
		case <-cancelled:
		default:
			close(cancelled)
		}
	}
	go maintainLease(ctx, cancel, Config{
		ForwardID: "forward-1", Client: client, RenewPeriod: time.Millisecond,
	}, current, &counters{})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("authorization failure did not close the tunnel")
	}
}

func TestMaintainLeaseRetriesIdempotentGenerationTotals(t *testing.T) {
	client := &fakeAPI{
		lease:       testLease(),
		renewErrors: []error{context.DeadlineExceeded, nil},
	}
	lease := client.lease
	current := &atomic.Pointer[api.PortForwardLease]{}
	current.Store(&lease)
	usage := &counters{}
	usage.bytesUp.Store(100)
	usage.bytesDown.Store(200)
	usage.connectionCount.Store(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go maintainLease(ctx, cancel, Config{
		ForwardID: "forward-1", Client: client, RenewPeriod: time.Millisecond,
	}, current, usage)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		count := len(client.renewRequests)
		client.mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.renewRequests) < 2 {
		t.Fatalf("expected a renew retry, got %d request(s)", len(client.renewRequests))
	}
	first := client.renewRequests[0]
	second := client.renewRequests[1]
	if first.BytesUpTotal != 100 || first.BytesDownTotal != 200 || first.ConnectionCountTotal != 2 {
		t.Fatalf("unexpected first generation totals: %#v", first)
	}
	if second != first {
		t.Fatalf("renew retry changed generation totals: first=%#v second=%#v", first, second)
	}
}

func TestMaintainLeaseFailsClosedForMissingOrChangedGeneration(t *testing.T) {
	for name, state := range map[string]struct {
		current  *api.PortForwardLease
		response api.PortForwardLease
	}{
		"missing lease": {current: nil, response: testLease()},
		"changed generation": {
			current:  func() *api.PortForwardLease { lease := testLease(); return &lease }(),
			response: func() api.PortForwardLease { lease := testLease(); lease.Generation = 2; return lease }(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeAPI{lease: state.response}
			pointer := &atomic.Pointer[api.PortForwardLease]{}
			if state.current != nil {
				pointer.Store(state.current)
			}
			cancelled := make(chan struct{})
			cancel := func() { close(cancelled) }
			go maintainLease(context.Background(), cancel, Config{
				ForwardID: "forward-1", Client: client, RenewPeriod: time.Millisecond,
			}, pointer, &counters{})
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("invalid lease state did not fail closed")
			}
		})
	}
}

func TestLeaseFailureDeadlineIncludesControlPlaneGrace(t *testing.T) {
	lease := testLease()
	lease.LeaseExpiresAt = time.Now().Add(-2 * time.Second).Format(time.RFC3339Nano)
	lease.ControlPlaneGraceSeconds = 5
	remaining := time.Until(leaseFailureDeadline(lease))
	if remaining < 2*time.Second || remaining > 4*time.Second {
		t.Fatalf("unexpected lease failure deadline: %s", remaining)
	}
}

func TestInvalidLeaseExpirationFailsClosed(t *testing.T) {
	lease := testLease()
	lease.LeaseExpiresAt = "not-a-timestamp"
	if err := validateLease("forward-1", lease); err == nil {
		t.Fatal("invalid lease expiration was accepted")
	}
	if deadline := leaseFailureDeadline(lease); time.Until(deadline) > 100*time.Millisecond {
		t.Fatalf("invalid lease expiration did not fail immediately: %s", deadline)
	}
}

func TestBandwidthLimiterHonorsCancellationWhileQueued(t *testing.T) {
	limiter := newBandwidthLimiter(1)
	if err := limiter.wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.wait(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected limiter cancellation: %v", err)
	}
	if err := newBandwidthLimiter(0).wait(context.Background(), 1024); err != nil {
		t.Fatal(err)
	}
}

func handshakeBytes(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	buffer := bytes.NewBufferString(protocolMagic)
	if err := binary.Write(buffer, binary.BigEndian, uint32(len(payload))); err != nil {
		t.Fatal(err)
	}
	buffer.Write(payload)
	return buffer.Bytes()
}

func readServerHandshake(t *testing.T, reader io.Reader) serverHandshake {
	t.Helper()
	magic := make([]byte, len(protocolMagic))
	if _, err := io.ReadFull(reader, magic); err != nil {
		t.Fatal(err)
	}
	if string(magic) != protocolMagic {
		t.Fatalf("unexpected handshake magic %q", magic)
	}
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	var response serverHandshake
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	return response
}
