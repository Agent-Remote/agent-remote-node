package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
