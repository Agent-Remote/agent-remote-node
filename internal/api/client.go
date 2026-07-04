package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
	Resources          ResourceStatus `json:"resources"`
	Runtime            RuntimeStatus  `json:"runtime"`
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
	DockerOK              bool `json:"docker_ok"`
	TmuxOK                bool `json:"tmux_ok"`
	ActiveSessions        int  `json:"active_sessions"`
	ActiveBrowserSessions int  `json:"active_browser_sessions"`
	Containers            int  `json:"containers"`
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
		SessionID       string  `json:"session_id"`
		TmuxSessionName string  `json:"tmux_session_name"`
		ContainerID     *string `json:"container_id"`
	} `json:"data"`
	RequestID string `json:"request_id"`
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s: %s", resp.Status, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
