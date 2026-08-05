package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const maxResponseBodyBytes = 1 << 20

// Client talks to agent-remote-server.
type Client struct {
	baseURL    string
	nodeToken  string
	httpClient *http.Client
}

// NewClient creates an API client.
func NewClient(baseURL string, nodeToken string) Client {
	return Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		nodeToken: nodeToken,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// RegisterNodeRequest is the node registration payload.
type RegisterNodeRequest struct {
	NodeID            string `json:"node_id"`
	RegistrationToken string `json:"registration_token"`
	Version           string `json:"version"`
}

// RegisterNodeResponse is the node registration response.
type RegisterNodeResponse struct {
	Data struct {
		NodeID    string `json:"node_id"`
		NodeToken string `json:"node_token"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// HeartbeatRequest is the node heartbeat payload.
type HeartbeatRequest struct {
	NodeID             string         `json:"node_id"`
	Version            string         `json:"version"`
	SupportedToolTypes []string       `json:"supported_tool_types"`
	WireGuardIP        string         `json:"wireguard_ip,omitempty"`
	WireGuardPublicKey string         `json:"wireguard_public_key,omitempty"`
	WireGuardEndpoint  string         `json:"wireguard_endpoint,omitempty"`
	Resources          ResourceStatus `json:"resources"`
	Runtime            RuntimeStatus  `json:"runtime"`
}

// WireGuardPeer is a device peer applied to the node interface.
type WireGuardPeer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
}

// WireGuardPeersResponse contains active device peers.
type WireGuardPeersResponse struct {
	Data struct {
		Items []WireGuardPeer `json:"items"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// ResourceStatus describes node resources.
type ResourceStatus struct {
	CPULoad          float64 `json:"cpu_load"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	DiskUsedBytes    int64   `json:"disk_used_bytes"`
	DiskTotalBytes   int64   `json:"disk_total_bytes"`
}

// RuntimeStatus describes node runtime state.
type RuntimeStatus struct {
	DockerOK              bool                `json:"docker_ok"`
	TmuxOK                bool                `json:"tmux_ok"`
	ActiveSessions        int                 `json:"active_sessions"`
	ActiveBrowserSessions int                 `json:"active_browser_sessions"`
	Containers            int                 `json:"containers"`
	RuntimeCapabilities   RuntimeCapabilities `json:"runtime_capabilities"`
}

// RuntimeCapabilities describes independently detected node runtimes.
type RuntimeCapabilities struct {
	Backends              []string                        `json:"backends"`
	Native                map[string]bool                 `json:"native"`
	DockerSandbox         map[string]bool                 `json:"docker_sandbox"`
	BrowserDocker         map[string]bool                 `json:"browser_docker"`
	Dependencies          map[string]string               `json:"dependencies"`
	ProbeErrors           []string                        `json:"probe_errors"`
	SessionPortForwarding SessionPortForwardingCapability `json:"session_port_forwarding"`
	DeviceControl         DeviceControlCapability         `json:"device_control"`
}

// SessionPortForwardingCapability describes the tunnel protocols and runtime backends the node can safely serve.
type SessionPortForwardingCapability struct {
	Supported        bool     `json:"supported"`
	ProtocolVersions []int    `json:"protocol_versions"`
	Backends         []string `json:"backends"`
	MaxStreams       int      `json:"max_streams"`
}

// DeviceControlCapability describes the managed proxy protocol available to remote sessions.
type DeviceControlCapability struct {
	Supported        bool     `json:"supported"`
	ProtocolVersions []int    `json:"protocol_versions"`
	Platforms        []string `json:"platforms"`
	Backends         []string `json:"backends"`
	Capabilities     []string `json:"capabilities"`
}

// DeviceRelayMaterialRequest registers one generation-scoped proxy certificate pin.
type DeviceRelayMaterialRequest struct {
	Generation uint64 `json:"generation"`
	SPKISHA256 string `json:"spki_sha256"`
}

// DeviceRelayMaterialResponse contains one-time peer material for the opaque relay.
type DeviceRelayMaterialResponse struct {
	Data struct {
		Status          string  `json:"status"`
		Role            string  `json:"role"`
		Generation      uint64  `json:"generation"`
		RelayPath       *string `json:"relay_path"`
		RelayTicket     *string `json:"relay_ticket"`
		PeerSPKISHA256  *string `json:"peer_spki_sha256"`
		ExporterContext *string `json:"exporter_context"`
		ExpiresAt       *string `json:"expires_at"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// TaskEnvelope is a leased task.
type TaskEnvelope struct {
	TaskID         string         `json:"task_id"`
	NodeID         string         `json:"node_id"`
	TaskType       string         `json:"task_type"`
	IdempotencyKey string         `json:"idempotency_key"`
	Payload        map[string]any `json:"payload"`
	LeaseUntil     string         `json:"lease_until"`
	CreatedAt      string         `json:"created_at"`
	ExpiresAt      string         `json:"expires_at"`
}

// PollTasksResponse contains leased tasks.
type PollTasksResponse struct {
	Data struct {
		Tasks []TaskEnvelope `json:"tasks"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// VerifyAttachRequest validates an SSH forced-command attach.
type VerifyAttachRequest struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
}

// VerifyAttachResponse is the attach verification response.
type VerifyAttachResponse struct {
	Data struct {
		SessionID         string  `json:"session_id"`
		TmuxSessionName   string  `json:"tmux_session_name"`
		ContainerID       *string `json:"container_id"`
		RuntimeBackend    string  `json:"runtime_backend"`
		RuntimeResourceID *string `json:"runtime_resource_id"`
		ForwardSSHAgent   bool    `json:"forward_ssh_agent"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// VerifyBindingAttachRequest validates an SSH forced-command binding attach.
type VerifyBindingAttachRequest struct {
	NodeID        string `json:"node_id"`
	ToolAccountID string `json:"tool_account_id"`
	DeviceID      string `json:"device_id"`
}

// VerifyBindingAttachResponse is the binding attach verification response.
type VerifyBindingAttachResponse struct {
	Data struct {
		BindingSessionID string `json:"binding_session_id"`
		TmuxSessionName  string `json:"tmux_session_name"`
		RuntimeBackend   string `json:"runtime_backend"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// VerifySyncRequest validates an SSH forced-command sync transport.
type VerifySyncRequest struct {
	NodeID   string `json:"node_id"`
	DeviceID string `json:"device_id"`
}

// VerifySyncResponse identifies the isolated user for a sync transport.
type VerifySyncResponse struct {
	Data struct {
		UserID string `json:"user_id"`
	} `json:"data"`
	RequestID string `json:"request_id"`
}

// RedeemPortForwardRequest exchanges a one-time tunnel token for a renewable lease.
type RedeemPortForwardRequest struct {
	ForwardID    string `json:"forward_id"`
	DeviceID     string `json:"device_id"`
	SSHKeyID     string `json:"ssh_key_id"`
	ConnectToken string `json:"connect_token"`
}

// PortForwardLease contains the fixed runtime target and authorization generation.
type PortForwardLease struct {
	ForwardID                string `json:"forward_id"`
	SessionID                string `json:"session_id"`
	RuntimeBackend           string `json:"runtime_backend"`
	RuntimeResourceID        string `json:"runtime_resource_id"`
	RemotePort               int    `json:"remote_port"`
	Generation               int    `json:"generation"`
	LeaseExpiresAt           string `json:"lease_expires_at"`
	MaxStreams               int    `json:"max_streams"`
	BytesPerSecond           int    `json:"bytes_per_second"`
	ControlPlaneGraceSeconds int    `json:"control_plane_grace_seconds"`
}

// PortForwardLeaseResponse wraps a port-forward authorization lease.
type PortForwardLeaseResponse struct {
	Data      PortForwardLease `json:"data"`
	RequestID string           `json:"request_id"`
}

// RenewPortForwardRequest reports generation totals while extending a lease.
type RenewPortForwardRequest struct {
	Generation           int   `json:"generation"`
	BytesUpTotal         int64 `json:"bytes_up_total"`
	BytesDownTotal       int64 `json:"bytes_down_total"`
	ConnectionCountTotal int64 `json:"connection_count_total"`
}

// ReleasePortForwardRequest reports final generation totals when a tunnel disconnects.
type ReleasePortForwardRequest struct {
	Generation           int    `json:"generation"`
	BytesUpTotal         int64  `json:"bytes_up_total"`
	BytesDownTotal       int64  `json:"bytes_down_total"`
	ConnectionCountTotal int64  `json:"connection_count_total"`
	Reason               string `json:"reason"`
}

// HTTPError is a bounded control-plane error suitable for authorization decisions.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

// Error returns a bounded description without response bodies or credentials.
func (e *HTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server returned HTTP %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("server returned HTTP %d", e.StatusCode)
}

// ReconcileRequest is a node reconciliation payload.
type ReconcileRequest struct {
	NodeID   string         `json:"node_id"`
	Sections []string       `json:"sections"`
	Snapshot map[string]any `json:"snapshot"`
}

// RegisterNode exchanges a registration token for a node token.
func (c Client) RegisterNode(ctx context.Context, request RegisterNodeRequest) (RegisterNodeResponse, error) {
	var response RegisterNodeResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/register", request, &response, false)
	return response, err
}

// SendHeartbeat submits a node heartbeat.
func (c Client) SendHeartbeat(ctx context.Context, request HeartbeatRequest) error {
	return c.do(ctx, http.MethodPost, "/api/v1/node-api/heartbeat", request, nil, true)
}

// ListWireGuardPeers returns active device peers for node synchronization.
func (c Client) ListWireGuardPeers(ctx context.Context) (WireGuardPeersResponse, error) {
	var response WireGuardPeersResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/node-api/wireguard/peers", nil, &response, true)
	return response, err
}

// PollTasks leases pending tasks.
func (c Client) PollTasks(ctx context.Context) (PollTasksResponse, error) {
	var response PollTasksResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/tasks/poll", map[string]any{}, &response, true)
	return response, err
}

// StartTask marks a task as running.
func (c Client) StartTask(ctx context.Context, taskID string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/node-api/tasks/"+taskID+"/start", map[string]any{}, nil, true)
}

// CompleteTask reports task success.
func (c Client) CompleteTask(ctx context.Context, taskID string, result map[string]any) error {
	return c.do(ctx, http.MethodPost, "/api/v1/node-api/tasks/"+taskID+"/complete", map[string]any{"result": result}, nil, true)
}

// FailTask reports task failure.
func (c Client) FailTask(ctx context.Context, taskID string, taskError map[string]any) error {
	return c.do(ctx, http.MethodPost, "/api/v1/node-api/tasks/"+taskID+"/fail", map[string]any{"error": taskError}, nil, true)
}

// VerifyAttach validates a forced-command attach request.
func (c Client) VerifyAttach(ctx context.Context, request VerifyAttachRequest) (VerifyAttachResponse, error) {
	var response VerifyAttachResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/attach/verify", request, &response, true)
	return response, err
}

// VerifyBindingAttach validates a forced-command account binding attach request.
func (c Client) VerifyBindingAttach(ctx context.Context, request VerifyBindingAttachRequest) (VerifyBindingAttachResponse, error) {
	var response VerifyBindingAttachResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/binding-attach/verify", request, &response, true)
	return response, err
}

// VerifySync validates a forced-command sync transport request.
func (c Client) VerifySync(ctx context.Context, request VerifySyncRequest) (VerifySyncResponse, error) {
	var response VerifySyncResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/sync/verify", request, &response, true)
	return response, err
}

// RedeemPortForward exchanges a one-time connection token for a lease.
func (c Client) RedeemPortForward(ctx context.Context, request RedeemPortForwardRequest) (PortForwardLeaseResponse, error) {
	var response PortForwardLeaseResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/port-forwards/redeem", request, &response, true)
	return response, err
}

// RenewPortForward extends a lease and reports aggregate usage deltas.
func (c Client) RenewPortForward(ctx context.Context, forwardID string, request RenewPortForwardRequest) (PortForwardLeaseResponse, error) {
	var response PortForwardLeaseResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/port-forwards/"+forwardID+"/renew", request, &response, true)
	return response, err
}

// ReleasePortForward releases a tunnel generation and reports final usage deltas.
func (c Client) ReleasePortForward(ctx context.Context, forwardID string, request ReleasePortForwardRequest) error {
	return c.do(ctx, http.MethodPost, "/api/v1/node-api/port-forwards/"+forwardID+"/release", request, nil, true)
}

// RegisterDeviceRelayMaterial exchanges a proxy certificate pin for one-time peer material.
func (c Client) RegisterDeviceRelayMaterial(ctx context.Context, deviceSessionID string, request DeviceRelayMaterialRequest) (DeviceRelayMaterialResponse, error) {
	var response DeviceRelayMaterialResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/node-api/device-sessions/"+url.PathEscape(deviceSessionID)+"/relay-material", request, &response, true)
	return response, err
}

// OpenDeviceRelay consumes a one-time ticket and opens the bounded binary WebSocket relay.
func (c Client) OpenDeviceRelay(ctx context.Context, deviceSessionID string, relayPath string, relayTicket string) (io.ReadWriteCloser, error) {
	expectedPath := "/api/v1/device-sessions/" + url.PathEscape(deviceSessionID) + "/relay"
	if relayPath != expectedPath || relayTicket == "" {
		return nil, errors.New("device relay endpoint or ticket is invalid")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("server URL cannot be used for a device relay")
	}
	websocketURL := *base
	if base.Scheme == "https" {
		websocketURL.Scheme = "wss"
	} else {
		websocketURL.Scheme = "ws"
	}
	websocketURL.Path = relayPath
	websocketURL.RawQuery = ""
	websocketURL.Fragment = ""
	origin := *base
	origin.Path = "/"
	origin.RawQuery = ""
	origin.Fragment = ""
	configuration, err := websocket.NewConfig(websocketURL.String(), origin.String())
	if err != nil {
		return nil, err
	}
	configuration.Header.Set("authorization", "Bearer "+relayTicket)
	connection, err := configuration.DialContext(ctx)
	if err != nil {
		return nil, err
	}
	connection.PayloadType = websocket.BinaryFrame
	connection.MaxPayloadBytes = 4 << 20
	return connection, nil
}

// Reconcile submits a reconciliation snapshot.
func (c Client) Reconcile(ctx context.Context, request ReconcileRequest) error {
	return c.do(ctx, http.MethodPost, "/api/v1/node-api/reconcile", request, nil, true)
}

func (c Client) do(ctx context.Context, method string, path string, payload any, out any, auth bool) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	if auth {
		req.Header.Set("authorization", "Bearer "+c.nodeToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBodyBytes {
		return errors.New("server response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = decodeResponseJSON(data, &envelope)
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Code:       envelope.Error.Code,
			Message:    envelope.Error.Message,
		}
	}
	if out == nil {
		return nil
	}
	return decodeResponseJSON(data, out)
}

func decodeResponseJSON(data []byte, out any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("server response must contain one JSON object")
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("server response contains trailing data")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("server response object key is invalid")
			}
			if _, exists := seen[key]; exists {
				return errors.New("server response contains a duplicate key")
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("server response object is incomplete")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("server response array is incomplete")
		}
	default:
		return errors.New("server response delimiter is invalid")
	}
	return nil
}
