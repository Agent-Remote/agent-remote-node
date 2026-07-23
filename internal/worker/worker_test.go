package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func TestWorkerRejectsUnknownTask(t *testing.T) {
	w := Worker{cfg: config.Config{}.WithDefaults()}
	_, err := w.executeKnownTask(context.Background(), api.TaskEnvelope{
		TaskID: "task_unknown", TaskType: "future_task", Payload: map[string]any{},
	})
	if !errors.Is(err, ErrUnsupportedTask) {
		t.Fatalf("expected ErrUnsupportedTask, got %v", err)
	}
}

func TestWorkerCleanupResourcesUsesRuntimeHelper(t *testing.T) {
	runtimeSocket, operations := startRuntimeHelperStub(t, map[string]any{
		"status": "cleaned", "cleaned_count": float64(1),
	})
	w := Worker{cfg: config.Config{RuntimeSocketPath: runtimeSocket}.WithDefaults()}
	result, err := w.executeKnownTask(context.Background(), api.TaskEnvelope{
		TaskID: "task_cleanup", TaskType: "cleanup_resources",
		Payload: map[string]any{"runtime_backend": "native", "session_ids": []any{"session_1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "cleaned" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if operation := <-operations; operation != "cleanup_resources" {
		t.Fatalf("unexpected helper operation %q", operation)
	}
}

func startRuntimeHelperStub(t *testing.T, result map[string]any) (string, <-chan string) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "ar-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := directory + "/runtime.sock"
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	operations := make(chan string, 1)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		var request runtimehelper.Request
		if json.NewDecoder(connection).Decode(&request) != nil {
			return
		}
		operations <- request.Operation
		_ = json.NewEncoder(connection).Encode(runtimehelper.Response{
			Version: runtimehelper.ProtocolVersion, OK: true, Result: result,
		})
	}()
	return path, operations
}

func TestWorkerPollOnceCompletesTask(t *testing.T) {
	var completed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{{
						"task_id":         "task_1",
						"node_id":         "node_1",
						"task_type":       "reconcile_state",
						"idempotency_key": "task_1",
						"payload":         map[string]any{},
						"lease_until":     "2026-07-04T00:00:30Z",
						"created_at":      "2026-07-04T00:00:00Z",
						"expires_at":      "2026-07-05T00:00:00Z",
					}},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_1/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/task_1/complete":
			completed = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	taskLedger, err := ledger.Open(t.TempDir() + "/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{NodeID: "node_1", ServerURL: server.URL, NodeToken: "node_token"}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected task completion")
	}
}

func TestWorkerSyncSSHKeysWritesAuthorizedKeys(t *testing.T) {
	var completed bool
	authorizedKeysPath := t.TempDir() + "/authorized_keys"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{{
						"task_id":         "task_ssh",
						"node_id":         "node_1",
						"task_type":       "sync_ssh_keys",
						"idempotency_key": "task_ssh",
						"payload": map[string]any{
							"device_id":  "dev_1",
							"session_id": "sess_1",
							"ssh_keys": []map[string]any{{
								"id":             "key_1",
								"public_key":     "ssh-ed25519 AAAATEST rem@test",
								"forced_command": "agent-remote-attach --session sess_1 --device dev_1",
							}},
						},
						"lease_until": "2026-07-04T00:00:30Z",
						"created_at":  "2026-07-04T00:00:00Z",
						"expires_at":  "2026-07-05T00:00:00Z",
					}},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_ssh/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/task_ssh/complete":
			completed = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	taskLedger, err := ledger.Open(t.TempDir() + "/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeID:                "node_1",
		ServerURL:             server.URL,
		NodeToken:             "node_token",
		SSHAuthorizedKeysPath: authorizedKeysPath,
		AttachBinaryPath:      "/usr/local/bin/agent-remote-attach",
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected task completion")
	}
	data, err := os.ReadFile(authorizedKeysPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/usr/local/bin/agent-remote-attach --session sess_1 --device dev_1") {
		t.Fatalf("authorized_keys missing forced command: %s", string(data))
	}
}

func TestWorkerPrepareWorkspaceCreatesDirectory(t *testing.T) {
	var completed bool
	runtimeSocket, operations := startRuntimeHelperStub(t, map[string]any{
		"status": "prepared", "remote_path": "/managed/workspace",
	})
	workspaceRoot := t.TempDir()
	remotePath := workspaceRoot + "/user_1/workspaces/workspace_1/files"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{{
						"task_id":         "task_workspace",
						"node_id":         "node_1",
						"task_type":       "prepare_workspace",
						"idempotency_key": "task_workspace",
						"payload": map[string]any{
							"user_id":         "user_1",
							"workspace_id":    "workspace_1",
							"sync_session_id": "sync_1",
							"remote_path":     remotePath,
						},
						"lease_until": "2026-07-04T00:00:30Z",
						"created_at":  "2026-07-04T00:00:00Z",
						"expires_at":  "2026-07-05T00:00:00Z",
					}},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_workspace/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/task_workspace/complete":
			completed = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	taskLedger, err := ledger.Open(t.TempDir() + "/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeID:            "node_1",
		ServerURL:         server.URL,
		NodeToken:         "node_token",
		WorkspaceRoot:     workspaceRoot,
		RuntimeSocketPath: runtimeSocket,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected task completion")
	}
	if operation := <-operations; operation != "prepare_workspace" {
		t.Fatalf("unexpected helper operation %q", operation)
	}
}

func TestWorkerCreateBindingSessionCompletesTask(t *testing.T) {
	var completed map[string]any
	runtimeSocket, operations := startRuntimeHelperStub(t, map[string]any{
		"status": "waiting_user_login", "runtime_backend": "docker_sandbox",
		"binding_session_id": "bind_1", "tool_account_id": "account_1",
	})
	accountRoot := t.TempDir()
	accountPath := accountRoot + "/user_1/accounts/account_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{{
						"task_id":         "task_bind",
						"node_id":         "node_1",
						"task_type":       "create_binding_session",
						"idempotency_key": "task_bind",
						"payload": map[string]any{
							"binding_id":          "bind_1",
							"tool_account_id":     "account_1",
							"tool_type":           "claude",
							"user_id":             "user_1",
							"region_code":         "US",
							"timezone":            "America/Los_Angeles",
							"locale":              "en_US.UTF-8",
							"account_remote_path": accountPath,
							"tmux_session_name":   "bind-claude",
							"template": map[string]any{
								"sandbox_agent": "claude",
								"command":       []string{"claude", "login"},
								"verifier":      "claude",
							},
							"verifier": "claude",
						},
						"lease_until": "2026-07-04T00:00:30Z",
						"created_at":  "2026-07-04T00:00:00Z",
						"expires_at":  "2026-07-05T00:00:00Z",
					}},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_bind/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/task_bind/complete":
			var body struct {
				Result map[string]any `json:"result"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			completed = body.Result
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	taskLedger, err := ledger.Open(t.TempDir() + "/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeID:            "node_1",
		ServerURL:         server.URL,
		NodeToken:         "node_token",
		AccountRoot:       accountRoot,
		DockerBinaryPath:  "docker",
		TmuxBinaryPath:    "agent-remote-missing-tmux",
		RuntimeSocketPath: runtimeSocket,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if completed == nil {
		t.Fatal("expected task completion")
	}
	if completed["status"] != "waiting_user_login" {
		t.Fatalf("unexpected result: %#v", completed)
	}
	if operation := <-operations; operation != "docker_prepare_account" {
		t.Fatalf("unexpected helper operation %q", operation)
	}
}

func TestWorkerCreateToolSessionCompletesTask(t *testing.T) {
	var completed map[string]any
	runtimeSocket, operations := startRuntimeHelperStub(t, map[string]any{
		"status": "running", "runtime_backend": "docker_sandbox",
		"session_id": "session_1", "runtime_resource_id": "sandbox_1",
	})
	workspaceRoot := t.TempDir()
	accountRoot := t.TempDir()
	workspacePath := workspaceRoot + "/user_1/workspaces/workspace_1/files"
	accountPath := accountRoot + "/user_1/accounts/account_1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{{
						"task_id":         "task_session",
						"node_id":         "node_1",
						"task_type":       "create_tool_session",
						"idempotency_key": "task_session",
						"payload": map[string]any{
							"session_id":            "session_1",
							"tool_account_id":       "account_1",
							"tool_type":             "claude",
							"user_id":               "user_1",
							"workspace_id":          "workspace_1",
							"project_key":           "sha256:project",
							"workspace_remote_path": workspacePath,
							"account_remote_path":   accountPath,
							"tmux_session_name":     "ar-claude-session",
							"sandbox_name":          "agent-remote-claude-session",
							"timezone":              "America/Los_Angeles",
							"locale":                "en_US.UTF-8",
							"argv":                  []string{"--model", "opus"},
							"template":              map[string]any{"sandbox_agent": "claude", "command": []string{"claude", "--model", "opus"}},
						},
						"lease_until": "2026-07-04T00:00:30Z",
						"created_at":  "2026-07-04T00:00:00Z",
						"expires_at":  "2026-07-05T00:00:00Z",
					}},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/task_session/complete":
			var body struct {
				Result map[string]any `json:"result"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			completed = body.Result
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	taskLedger, err := ledger.Open(t.TempDir() + "/ledger.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeID:            "node_1",
		ServerURL:         server.URL,
		NodeToken:         "node_token",
		WorkspaceRoot:     workspaceRoot,
		AccountRoot:       accountRoot,
		DockerBinaryPath:  "docker",
		TmuxBinaryPath:    "agent-remote-missing-tmux",
		RuntimeSocketPath: runtimeSocket,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if completed == nil {
		t.Fatal("expected task completion")
	}
	if completed["status"] != "running" {
		t.Fatalf("unexpected result: %#v", completed)
	}
	if completed["session_id"] != "session_1" {
		t.Fatalf("unexpected session result: %#v", completed)
	}
	if operation := <-operations; operation != "docker_start_session" {
		t.Fatalf("unexpected helper operation %q", operation)
	}
}
