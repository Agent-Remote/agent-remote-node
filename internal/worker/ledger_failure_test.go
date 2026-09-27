package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

func TestWorkerDoesNotReportFailureBeforeLedgerPublication(t *testing.T) {
	var starts, reports atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/node-api/tasks/original/start" {
			starts.Add(1)
		} else {
			reports.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "ledger.json")
	taskLedger, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{NodeID: "node_1", ServerURL: server.URL, NodeToken: "ledger-test"}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, cfg.NodeToken), taskLedger)
	task := api.TaskEnvelope{TaskID: "original", TaskType: "unsupported-test-task"}
	if err := w.executeTask(context.Background(), task); err == nil {
		t.Fatal("unpersisted failure was accepted")
	}
	if starts.Load() != 1 || reports.Load() != 0 {
		t.Fatal("worker reported failure without its durable result")
	}
	if _, ok, err := taskLedger.Get(task.TaskID); err != nil || ok {
		t.Fatal("failed publication created a cached terminal outcome", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := w.executeTask(context.Background(), task); err != nil {
		t.Fatal("repair did not permit exact task completion", err)
	}
	if reports.Load() != 1 {
		t.Fatal("repaired task did not report its persisted failure")
	}
}
