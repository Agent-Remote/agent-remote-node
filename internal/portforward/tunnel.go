// Package portforward serves restricted session loopback tunnels over SSH stdio.
package portforward

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"golang.org/x/net/http2"
)

const (
	protocolMagic           = "ARPF\x00\x01"
	maxHandshakePayload     = 8 << 10
	defaultDialTimeout      = 3 * time.Second
	defaultRenewPeriod      = 20 * time.Second
	defaultHandshakeTimeout = 10 * time.Second
)

// API exchanges tunnel credentials and maintains authorization leases.
type API interface {
	RedeemPortForward(context.Context, api.RedeemPortForwardRequest) (api.PortForwardLeaseResponse, error)
	RenewPortForward(context.Context, string, api.RenewPortForwardRequest) (api.PortForwardLeaseResponse, error)
	ReleasePortForward(context.Context, string, api.ReleasePortForwardRequest) error
}

// RuntimeDialer asks the privileged helper for a connected runtime socket.
type RuntimeDialer interface {
	DialSessionLoopback(context.Context, string, runtimehelper.DialSessionLoopbackPayload) (net.Conn, error)
}

// Config defines one SSH-authenticated tunnel connection.
type Config struct {
	ForwardID   string
	DeviceID    string
	SSHKeyID    string
	Client      API
	Runtime     RuntimeDialer
	Connection  net.Conn
	DialTimeout time.Duration
	RenewPeriod time.Duration
}

type clientHandshake struct {
	ForwardID     string `json:"forward_id"`
	ConnectToken  string `json:"connect_token"`
	ClientVersion string `json:"client_version"`
	MaxStreams    int    `json:"max_streams"`
}

type serverHandshake struct {
	OK                bool   `json:"ok"`
	Protocol          int    `json:"protocol,omitempty"`
	LeaseExpiresAt    string `json:"lease_expires_at,omitempty"`
	MaxStreams        int    `json:"max_streams,omitempty"`
	MaxBytesPerSecond int    `json:"max_bytes_per_second,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
}

type counters struct {
	bytesUp         atomic.Int64
	bytesDown       atomic.Int64
	connectionCount atomic.Int64
}

type handler struct {
	ctx         context.Context
	forwardID   string
	generation  int
	lease       api.PortForwardLease
	runtime     RuntimeDialer
	semaphore   chan struct{}
	dialTimeout time.Duration
	counters    *counters
	streamID    atomic.Uint64
	upload      *bandwidthLimiter
	download    *bandwidthLimiter
}

// Serve authenticates one tunnel and serves multiplexed CONNECT streams until it closes.
func Serve(ctx context.Context, config Config) error {
	if config.Client == nil || config.Runtime == nil || config.Connection == nil {
		return errors.New("port forward client, runtime, and connection are required")
	}
	if config.ForwardID == "" || config.DeviceID == "" || config.SSHKeyID == "" {
		return errors.New("port forward identity is required")
	}
	if config.DialTimeout <= 0 {
		config.DialTimeout = defaultDialTimeout
	}
	if config.RenewPeriod <= 0 {
		config.RenewPeriod = defaultRenewPeriod
	}
	_ = config.Connection.SetDeadline(time.Now().Add(defaultHandshakeTimeout))
	handshake, err := readClientHandshake(config.Connection)
	if err != nil {
		_ = writeServerHandshake(config.Connection, serverHandshake{OK: false, ErrorCode: "AUTH_INVALID"})
		return err
	}
	if handshake.ForwardID != config.ForwardID || len(handshake.ConnectToken) < 32 || len(handshake.ConnectToken) > 256 {
		_ = writeServerHandshake(config.Connection, serverHandshake{OK: false, ErrorCode: "AUTH_INVALID"})
		return errors.New("port forward handshake identity is invalid")
	}
	redeemContext, cancelRedeem := context.WithTimeout(ctx, 10*time.Second)
	response, err := config.Client.RedeemPortForward(redeemContext, api.RedeemPortForwardRequest{
		ForwardID: config.ForwardID, DeviceID: config.DeviceID,
		SSHKeyID: config.SSHKeyID, ConnectToken: handshake.ConnectToken,
	})
	cancelRedeem()
	if err != nil {
		code := publicErrorCode(err)
		_ = writeServerHandshake(config.Connection, serverHandshake{OK: false, ErrorCode: code})
		return fmt.Errorf("redeem port forward: %w", err)
	}
	lease := response.Data
	if err := validateLease(config.ForwardID, lease); err != nil {
		_ = writeServerHandshake(config.Connection, serverHandshake{OK: false, ErrorCode: "PROTOCOL_UNSUPPORTED"})
		return err
	}
	maxStreams := lease.MaxStreams
	if handshake.MaxStreams > 0 && handshake.MaxStreams < maxStreams {
		maxStreams = handshake.MaxStreams
	}
	if err := writeServerHandshake(config.Connection, serverHandshake{
		OK: true, Protocol: 1, LeaseExpiresAt: lease.LeaseExpiresAt,
		MaxStreams: maxStreams, MaxBytesPerSecond: lease.BytesPerSecond,
	}); err != nil {
		return err
	}
	_ = config.Connection.SetDeadline(time.Time{})
	tunnelContext, cancelTunnel := context.WithCancel(ctx)
	defer cancelTunnel()
	usage := &counters{}
	currentLease := &atomic.Pointer[api.PortForwardLease]{}
	currentLease.Store(&lease)
	go maintainLease(tunnelContext, cancelTunnel, config, currentLease, usage)
	go func() {
		<-tunnelContext.Done()
		_ = config.Connection.Close()
	}()
	handler := &handler{
		ctx: tunnelContext, forwardID: config.ForwardID, generation: lease.Generation,
		lease: lease, runtime: config.Runtime, semaphore: make(chan struct{}, maxStreams),
		dialTimeout: config.DialTimeout, counters: usage,
		upload:   newBandwidthLimiter(lease.BytesPerSecond),
		download: newBandwidthLimiter(lease.BytesPerSecond),
	}
	server := http2.Server{MaxConcurrentStreams: uint32(maxStreams), MaxReadFrameSize: 1 << 20}
	server.ServeConn(config.Connection, &http2.ServeConnOpts{Context: tunnelContext, Handler: handler})
	cancelTunnel()
	finalLease := currentLease.Load()
	if finalLease == nil {
		finalLease = &lease
	}
	releaseContext, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRelease()
	totals := snapshotCounters(usage)
	releasePortForward(releaseContext, config.Client, config.ForwardID, api.ReleasePortForwardRequest{
		Generation: finalLease.Generation, BytesUpTotal: totals.BytesUpTotal,
		BytesDownTotal: totals.BytesDownTotal, ConnectionCountTotal: totals.ConnectionCountTotal,
		Reason: "ssh_disconnected",
	})
	return nil
}

func releasePortForward(ctx context.Context, client API, forwardID string, request api.ReleasePortForwardRequest) {
	for attempt := 0; attempt < 3; attempt++ {
		if client.ReleasePortForward(ctx, forwardID, request) == nil {
			return
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

func (h *handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !validStreamRequest(request, h.forwardID) {
		http.Error(response, "invalid tunnel stream", http.StatusBadRequest)
		return
	}
	if h.ctx.Err() != nil {
		http.Error(response, "tunnel expired", http.StatusServiceUnavailable)
		return
	}
	select {
	case h.semaphore <- struct{}{}:
		defer func() { <-h.semaphore }()
	default:
		http.Error(response, "stream limit reached", http.StatusTooManyRequests)
		return
	}
	dialContext, cancelDial := context.WithTimeout(h.ctx, h.dialTimeout)
	requestID := fmt.Sprintf("pf-%s-%d", h.forwardID, h.streamID.Add(1))
	connection, err := h.runtime.DialSessionLoopback(dialContext, requestID, runtimehelper.DialSessionLoopbackPayload{
		SessionID: h.lease.SessionID, RuntimeBackend: h.lease.RuntimeBackend, Port: h.lease.RemotePort,
	})
	cancelDial()
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		http.Error(response, "runtime connection failed", status)
		return
	}
	defer connection.Close()
	h.counters.connectionCount.Add(1)
	response.WriteHeader(http.StatusOK)
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
	closeOnCancel := make(chan struct{})
	go func() {
		select {
		case <-h.ctx.Done():
			_ = connection.Close()
		case <-closeOnCancel:
		}
	}()
	defer close(closeOnCancel)
	downloadDone := make(chan int64, 1)
	go func() {
		written, _ := io.Copy(&countingWriter{
			writer: &flushWriter{writer: response}, counter: &h.counters.bytesDown,
			limiter: h.download, ctx: h.ctx,
		}, connection)
		downloadDone <- written
	}()
	_, _ = io.Copy(&countingWriter{
		writer: connection, counter: &h.counters.bytesUp, limiter: h.upload, ctx: h.ctx,
	}, request.Body)
	if tcpConnection, ok := connection.(*net.TCPConn); ok {
		_ = tcpConnection.CloseWrite()
	}
	<-downloadDone
}

func validStreamRequest(request *http.Request, forwardID string) bool {
	return request.Method == http.MethodConnect &&
		request.Host == "session-loopback" &&
		validStreamHeaders(request.Header) &&
		len(request.Header.Values("x-agent-remote-forward-id")) == 1 &&
		request.Header.Get("x-agent-remote-forward-id") == forwardID
}

func validStreamHeaders(headers http.Header) bool {
	for name := range headers {
		switch http.CanonicalHeaderKey(name) {
		case "X-Agent-Remote-Forward-Id", "Content-Length", "User-Agent", "Accept-Encoding":
		default:
			return false
		}
	}
	return true
}

func maintainLease(
	ctx context.Context,
	cancel context.CancelFunc,
	config Config,
	currentLease *atomic.Pointer[api.PortForwardLease],
	usage *counters,
) {
	ticker := time.NewTicker(config.RenewPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		lease := currentLease.Load()
		if lease == nil {
			cancel()
			return
		}
		totals := snapshotCounters(usage)
		renewContext, cancelRenew := context.WithTimeout(ctx, 10*time.Second)
		response, err := config.Client.RenewPortForward(renewContext, config.ForwardID, api.RenewPortForwardRequest{
			Generation: lease.Generation, BytesUpTotal: totals.BytesUpTotal,
			BytesDownTotal: totals.BytesDownTotal, ConnectionCountTotal: totals.ConnectionCountTotal,
		})
		cancelRenew()
		if err == nil {
			if validateLease(config.ForwardID, response.Data) != nil || response.Data.Generation != lease.Generation {
				cancel()
				return
			}
			currentLease.Store(&response.Data)
			continue
		}
		if isAuthorizationFailure(err) || time.Now().After(leaseFailureDeadline(*lease)) {
			cancel()
			return
		}
	}
}

func validateLease(forwardID string, lease api.PortForwardLease) error {
	if lease.ForwardID != forwardID || lease.SessionID == "" || lease.RuntimeBackend != "native" ||
		lease.RuntimeResourceID == "" || lease.RemotePort < 1 || lease.RemotePort > 65535 ||
		lease.Generation < 1 || lease.MaxStreams < 1 || lease.MaxStreams > 1024 ||
		lease.BytesPerSecond < 0 || lease.ControlPlaneGraceSeconds < 0 {
		return errors.New("control plane returned an invalid port forward lease")
	}
	if _, err := time.Parse(time.RFC3339Nano, lease.LeaseExpiresAt); err != nil {
		return errors.New("control plane returned an invalid lease expiration")
	}
	return nil
}

func leaseFailureDeadline(lease api.PortForwardLease) time.Time {
	expiresAt, err := time.Parse(time.RFC3339Nano, lease.LeaseExpiresAt)
	if err != nil {
		return time.Now()
	}
	return expiresAt.Add(time.Duration(lease.ControlPlaneGraceSeconds) * time.Second)
}

func isAuthorizationFailure(err error) bool {
	var httpError *api.HTTPError
	return errors.As(err, &httpError) && httpError.StatusCode >= 400 && httpError.StatusCode < 500
}

func publicErrorCode(err error) string {
	var httpError *api.HTTPError
	if errors.As(err, &httpError) && httpError.Code != "" {
		return httpError.Code
	}
	return "CONTROL_PLANE_UNAVAILABLE"
}

func readClientHandshake(reader io.Reader) (clientHandshake, error) {
	magic := make([]byte, len(protocolMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != protocolMagic {
		return clientHandshake{}, errors.New("port forward handshake magic is invalid")
	}
	var payloadLength uint32
	if err := binary.Read(reader, binary.BigEndian, &payloadLength); err != nil {
		return clientHandshake{}, errors.New("port forward handshake length is invalid")
	}
	if payloadLength == 0 || payloadLength > maxHandshakePayload {
		return clientHandshake{}, errors.New("port forward handshake payload is invalid")
	}
	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return clientHandshake{}, errors.New("port forward handshake payload is incomplete")
	}
	if err := rejectDuplicateObjectKeys(payload); err != nil {
		return clientHandshake{}, errors.New("port forward handshake payload is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var handshake clientHandshake
	if err := decoder.Decode(&handshake); err != nil {
		return clientHandshake{}, errors.New("port forward handshake payload is invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return clientHandshake{}, errors.New("port forward handshake has trailing data")
	}
	if handshake.MaxStreams < 1 || handshake.MaxStreams > 1024 || len(handshake.ClientVersion) > 64 {
		return clientHandshake{}, errors.New("port forward handshake limits are invalid")
	}
	return handshake, nil
}

func rejectDuplicateObjectKeys(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("JSON value is not an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("JSON object key is invalid")
		}
		if _, exists := seen[key]; exists {
			return errors.New("JSON object contains a duplicate key")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.New("JSON object is incomplete")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON object has trailing data")
	}
	return nil
}

func writeServerHandshake(writer io.Writer, handshake serverHandshake) error {
	payload, err := json.Marshal(handshake)
	if err != nil {
		return err
	}
	if len(payload) > maxHandshakePayload {
		return errors.New("port forward handshake response is too large")
	}
	buffer := bufio.NewWriter(writer)
	if _, err := buffer.WriteString(protocolMagic); err != nil {
		return err
	}
	if err := binary.Write(buffer, binary.BigEndian, uint32(len(payload))); err != nil {
		return err
	}
	if _, err := buffer.Write(payload); err != nil {
		return err
	}
	return buffer.Flush()
}

type counterTotals = api.RenewPortForwardRequest

func snapshotCounters(value *counters) counterTotals {
	return counterTotals{
		BytesUpTotal: value.bytesUp.Load(), BytesDownTotal: value.bytesDown.Load(),
		ConnectionCountTotal: value.connectionCount.Load(),
	}
}

type countingWriter struct {
	writer  io.Writer
	counter *atomic.Int64
	limiter *bandwidthLimiter
	ctx     context.Context
}

func (w *countingWriter) Write(payload []byte) (int, error) {
	if err := w.limiter.wait(w.ctx, len(payload)); err != nil {
		return 0, err
	}
	written, err := w.writer.Write(payload)
	w.counter.Add(int64(written))
	return written, err
}

type flushWriter struct {
	writer http.ResponseWriter
}

func (w *flushWriter) Write(payload []byte) (int, error) {
	written, err := w.writer.Write(payload)
	if flusher, ok := w.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return written, err
}

type bandwidthLimiter struct {
	bytesPerSecond int
	mu             sync.Mutex
	next           time.Time
}

func newBandwidthLimiter(bytesPerSecond int) *bandwidthLimiter {
	return &bandwidthLimiter{bytesPerSecond: bytesPerSecond}
}

func (l *bandwidthLimiter) wait(ctx context.Context, size int) error {
	if l.bytesPerSecond <= 0 || size <= 0 {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	start := now
	if l.next.After(start) {
		start = l.next
	}
	duration := time.Duration(float64(size) / float64(l.bytesPerSecond) * float64(time.Second))
	l.next = start.Add(duration)
	l.mu.Unlock()
	wait := time.Until(start)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
