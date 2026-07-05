package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

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
		NodeID:        "node_1",
		ServerURL:     server.URL,
		NodeToken:     "node_token",
		WorkspaceRoot: workspaceRoot,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected task completion")
	}
	if _, err := os.Stat(remotePath + "/.agent-remote-workspace.json"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCreateBindingSessionCompletesTask(t *testing.T) {
	var completed map[string]any
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
		NodeID:           "node_1",
		ServerURL:        server.URL,
		NodeToken:        "node_token",
		AccountRoot:      accountRoot,
		DockerBinaryPath: "docker",
		TmuxBinaryPath:   "agent-remote-missing-tmux",
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
	if _, err := os.Stat(accountPath + "/.agent-remote-tool-account.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(accountPath + "/.claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(accountPath + "/.claude.json"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCreateToolSessionCompletesTask(t *testing.T) {
	var completed map[string]any
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
		NodeID:           "node_1",
		ServerURL:        server.URL,
		NodeToken:        "node_token",
		WorkspaceRoot:    workspaceRoot,
		AccountRoot:      accountRoot,
		DockerBinaryPath: "docker",
		TmuxBinaryPath:   "agent-remote-missing-tmux",
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
	if _, err := os.Stat(workspacePath + "/.agent-remote-session.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(accountPath + "/.claude"); err != nil {
		t.Fatal(err)
	}
}
