package skilllifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

type lifecycleFixture struct {
	URL                   string        `json:"url"`
	Token                 string        `json:"token"`
	UserToken             string        `json:"user_token"`
	NodeID                string        `json:"node_id"`
	UserID                string        `json:"user_id"`
	AccountID             string        `json:"account_id"`
	WorkspaceID           string        `json:"workspace_id"`
	OperationID           string        `json:"operation_id"`
	SSHExport             bool          `json:"ssh_export"`
	CLILifecycle          bool          `json:"cli_lifecycle"`
	ExportQuota           bool          `json:"export_quota"`
	ExportCapacity        bool          `json:"export_capacity"`
	ExportBytes           bool          `json:"export_bytes"`
	ExportOversize        bool          `json:"export_oversize"`
	ExportLong            bool          `json:"export_long"`
	ExportDestinationFull bool          `json:"export_destination_full"`
	ExportOverEntries     bool          `json:"export_over_entries"`
	HTTPTimeout           time.Duration `json:"-"`
}

type lifecycleResponse struct {
	Status string `json:"status"`
	Data   struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func readFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	data, err := os.ReadFile("/proof/private/fixture.json")
	if err != nil {
		t.Fatal("read disposable connection fixture")
	}
	var fixture lifecycleFixture
	if json.Unmarshal(data, &fixture) != nil || fixture.URL == "" || fixture.Token == "" || fixture.UserToken == "" {
		t.Fatal("invalid disposable connection fixture")
	}
	return fixture
}

func (f lifecycleFixture) request(ctx context.Context, method, path string, payload any) (lifecycleResponse, error) {
	var result lifecycleResponse
	data, err := json.Marshal(payload)
	if err != nil {
		return result, errors.New("encode lifecycle request")
	}
	request, err := http.NewRequestWithContext(ctx, method, f.URL+path, bytes.NewReader(data))
	if err != nil {
		return result, errors.New("construct lifecycle request")
	}
	request.Header.Set("Authorization", "Bearer "+f.UserToken)
	request.Header.Set("Content-Type", "application/json")
	timeout := f.HTTPTimeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return result, errors.New("lifecycle HTTP transport failed")
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &result) != nil {
		return result, errors.New("invalid lifecycle HTTP response")
	}
	if response.StatusCode != http.StatusOK {
		// Do not echo bodies, payloads or token-bearing configuration on failure.
		code := "unknown"
		switch result.Error.Code {
		case "STATE_PENDING", "STATE_QUOTA_EXCEEDED", "STATE_EXPIRED", "SKILL_MANAGER_UNSUPPORTED", "NODE_OFFLINE", "NODE_UNAVAILABLE", "TOOL_ACCOUNT_BUSY", "TOOL_ACCOUNT_NOT_READY", "RUNTIME_RECOVERY_REQUIRED", "MIGRATION_PENDING", "SKILL_PREPARATION_PENDING":
			code = result.Error.Code
		case "SKILL_MANAGER_DISABLED", "STATE_MIGRATION_REQUIRED", "PROJECT_KEY_MISMATCH", "SESSION_REPLACEMENT_INVALID", "WORKSPACE_NOT_PREPARED", "TOOL_ACCOUNT_MISMATCH", "TOOL_ACCOUNT_NOT_ACTIVE":
			code = result.Error.Code
		}
		return result, fmt.Errorf("lifecycle HTTP status %d code=%s", response.StatusCode, code)
	}
	return result, nil
}

func await(t *testing.T, ctx context.Context, description string, check func() bool) {
	t.Helper()
	awaitWithin(t, ctx, 2*time.Minute, description, check)
}

func awaitWithin(t *testing.T, ctx context.Context, timeout time.Duration, description string, check func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if check() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out: " + description)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
