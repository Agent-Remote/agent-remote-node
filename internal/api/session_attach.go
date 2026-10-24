package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// SessionAttachCapability describes installed exact session/device/key verification.
type SessionAttachCapability struct {
	Supported       bool `json:"supported"`
	ProtocolVersion int  `json:"protocol_version"`
}

// VerifyExactAttachRequest binds the session to the actual forced-command identity.
type VerifyExactAttachRequest struct {
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
	SSHKeyID  string `json:"ssh_key_id"`
}

// Validate rejects noncanonical or empty identities before contacting the Server.
func (r VerifyExactAttachRequest) Validate() error {
	for _, value := range []string{r.NodeID, r.SessionID, r.DeviceID, r.SSHKeyID} {
		if !canonicalAttachUUID(value) {
			return errors.New("exact session attachment requires canonical nonzero UUIDs")
		}
	}
	return nil
}

// ExactAttachData is the complete verified runtime target; resource fields may be null.
type ExactAttachData struct {
	Version           int     `json:"version"`
	ToolType          string  `json:"tool_type"`
	SessionID         string  `json:"session_id"`
	NodeID            string  `json:"node_id"`
	DeviceID          string  `json:"device_id"`
	SSHKeyID          string  `json:"ssh_key_id"`
	TmuxSessionName   string  `json:"tmux_session_name"`
	ContainerID       *string `json:"container_id"`
	RuntimeBackend    string  `json:"runtime_backend"`
	RuntimeResourceID *string `json:"runtime_resource_id"`
	ForwardSSHAgent   bool    `json:"forward_ssh_agent"`
}

// UnmarshalJSON requires every exact field and rejects ambiguous JSON encodings.
func (r *ExactAttachData) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	names := []string{"version", "tool_type", "session_id", "node_id", "device_id", "ssh_key_id", "tmux_session_name", "container_id", "runtime_backend", "runtime_resource_id", "forward_ssh_agent"}
	if len(fields) != len(names) {
		return errors.New("invalid exact attach fields")
	}
	for _, name := range names {
		value, exists := fields[name]
		if !exists || (name != "container_id" && name != "runtime_resource_id" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return errors.New("missing exact attach field")
		}
	}
	type plain ExactAttachData
	return json.Unmarshal(data, (*plain)(r))
}

// VerifyExactAttachResponse contains the verified target in the standard envelope.
type VerifyExactAttachResponse struct {
	Data      ExactAttachData `json:"data"`
	RequestID string          `json:"request_id"`
}

// UnmarshalJSON rejects aliased or ambiguous envelope fields as well as invalid data.
func (r *VerifyExactAttachResponse) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields["data"] == nil {
		return errors.New("missing exact attach data")
	}
	for name := range fields {
		if name != "data" && name != "request_id" {
			return errors.New("invalid exact attach envelope")
		}
	}
	type plain VerifyExactAttachResponse
	return json.Unmarshal(data, (*plain)(r))
}

// VerifyExactAttach verifies once without redirects or fallback to device-only routes.
func (c Client) VerifyExactAttach(ctx context.Context, request VerifyExactAttachRequest) (VerifyExactAttachResponse, error) {
	var response VerifyExactAttachResponse
	if err := request.Validate(); err != nil {
		return response, err
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.httpClient = &client
	if err := c.do(ctx, http.MethodPost, "/api/v1/node-api/attach/exact/verify", request, &response, true); err != nil {
		return VerifyExactAttachResponse{}, errors.New("exact session authorization failed")
	}
	d := response.Data
	if d.Version != 1 || d.ToolType != "claude" || d.SessionID != request.SessionID || d.NodeID != request.NodeID || d.DeviceID != request.DeviceID || d.SSHKeyID != request.SSHKeyID ||
		(d.RuntimeBackend != "native" && d.RuntimeBackend != "docker_sandbox") || !safeAttachRuntimeName(d.TmuxSessionName) ||
		(d.ContainerID != nil && !safeAttachRuntimeName(*d.ContainerID)) || (d.RuntimeResourceID != nil && !safeAttachRuntimeName(*d.RuntimeResourceID)) {
		return VerifyExactAttachResponse{}, errors.New("exact session authorization returned an invalid target")
	}
	return response, nil
}

func canonicalAttachUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func safeAttachRuntimeName(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == ':') {
			return false
		}
	}
	return true
}
