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
