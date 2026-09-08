package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowser"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func TestWorkerRunKeepsHeartbeatIndependentFromBlockedPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtimeSocketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ar-runtime-%d.sock", time.Now().UnixNano()))
	runtimeListener, err := net.Listen("unix", runtimeSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeListener.Close()
	defer os.Remove(runtimeSocketPath)
	go func() {
		for {
			connection, acceptErr := runtimeListener.Accept()
			if acceptErr != nil {
				return
			}
			var request runtimehelper.Request
			_ = json.NewDecoder(connection).Decode(&request)
			_ = json.NewEncoder(connection).Encode(runtimehelper.Response{
				Version: runtimehelper.ProtocolVersion, OK: true,
				Result: map[string]any{"sessions": []any{}},
			})
			_ = connection.Close()
		}
	}()
	var heartbeats atomic.Int32
	var reconciliations atomic.Int32
	pollRelease := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/heartbeat":
			w.WriteHeader(http.StatusOK)
			if heartbeats.Add(1) == 3 {
				close(pollRelease)
				cancel()
			}
		case "/api/v1/node-api/tasks/poll":
			<-pollRelease
		case "/api/v1/node-api/reconcile":
			reconciliations.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
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
		RuntimeSocketPath: runtimeSocketPath,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)

	if err := w.run(ctx, 10*time.Millisecond, 10*time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if got := heartbeats.Load(); got < 3 {
		t.Fatalf("expected heartbeats to continue during blocked polling, got %d", got)
	}
	if got := reconciliations.Load(); got < 2 {
		t.Fatalf("expected reconciliation to remain periodic, got %d", got)
	}
}

func TestWorkerRunReconcilesAtPollInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtimeSocketPath := filepath.Join(os.TempDir(), fmt.Sprintf("ar-runtime-%d.sock", time.Now().UnixNano()))
	runtimeListener, err := net.Listen("unix", runtimeSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeListener.Close()
	defer os.Remove(runtimeSocketPath)
	go func() {
		for {
			connection, acceptErr := runtimeListener.Accept()
			if acceptErr != nil {
				return
			}
			var request runtimehelper.Request
			_ = json.NewDecoder(connection).Decode(&request)
			_ = json.NewEncoder(connection).Encode(runtimehelper.Response{
				Version: runtimehelper.ProtocolVersion, OK: true,
				Result: map[string]any{"sessions": []any{}},
			})
			_ = connection.Close()
		}
	}()

	var heartbeats atomic.Int32
	var reconciliations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/heartbeat":
			heartbeats.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/poll":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/reconcile":
			if reconciliations.Add(1) == 3 {
				cancel()
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
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
		RuntimeSocketPath: runtimeSocketPath,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)

	if err := w.run(ctx, time.Second, 10*time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if got := reconciliations.Load(); got != 3 {
		t.Fatalf("expected three reconciliations, got %d", got)
	}
	if got := heartbeats.Load(); got != 1 {
		t.Fatalf("reconciliation used heartbeat interval: heartbeat calls=%d", got)
	}
}

func TestRunOperationLoopRetriesAfterFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runOperationLoop(ctx, "test operation", 5*time.Millisecond, false, func(context.Context) error {
			attempt := attempts.Add(1)
			if attempt <= 2 {
				return errors.New("temporary failure")
			}
			cancel()
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation loop did not recover")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected three attempts, got %d", got)
	}
}

func TestEgoBrowserBrokerLogCodeOmitsWrappedErrorContent(t *testing.T) {
	secret := "browser-content-secret"
	tests := []struct {
		err  error
		want string
	}{
		{err: fmt.Errorf("socket failure: %s", secret), want: "broker_error"},
		{err: fmt.Errorf("%w: %s", egobrowser.ErrProtocol, secret), want: "protocol_error"},
	}
	for _, test := range tests {
		got := egoBrowserBrokerLogCode(test.err)
		if got != test.want {
			t.Fatalf("broker error code = %q, want %q", got, test.want)
		}
		if strings.Contains(got, secret) {
			t.Fatalf("broker error code contains wrapped error content: %q", got)
		}
	}
	message := egoBrowserBrokerError("initialize ego-browser broker", fmt.Errorf("socket failure: %s", secret)).Error()
	if message != "initialize ego-browser broker: broker_error" {
		t.Fatalf("unexpected bounded broker error %q", message)
	}
	if strings.Contains(message, secret) {
		t.Fatalf("bounded broker error contains wrapped error content: %q", message)
	}
}

func TestRetryDelayIsExponentialAndBounded(t *testing.T) {
	for _, test := range []struct {
		failures int
		want     time.Duration
	}{
		{failures: 1, want: time.Second},
		{failures: 2, want: 2 * time.Second},
		{failures: 3, want: 4 * time.Second},
		{failures: 8, want: 30 * time.Second},
	} {
		if got := retryDelay(test.failures, time.Minute, time.Second, 30*time.Second); got != test.want {
			t.Fatalf("failure %d: expected %s, got %s", test.failures, test.want, got)
		}
	}
	if got := retryDelay(4, 5*time.Second, time.Second, 30*time.Second); got != 5*time.Second {
		t.Fatalf("expected interval cap, got %s", got)
	}
}

func TestHeartbeatDoesNotDependOnWireGuardSync(t *testing.T) {
	var heartbeatCalls atomic.Int32
	var peerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/heartbeat":
			heartbeatCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/wireguard/peers":
			peerCalls.Add(1)
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		NodeID:             "node_1",
		ServerURL:          server.URL,
		NodeToken:          "node_token",
		WireGuardPublicKey: "configured",
		RuntimeSocketPath:  t.TempDir() + "/missing.sock",
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), nil)
	if err := w.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.syncWireGuardPeers(context.Background()); err == nil {
		t.Fatal("expected WireGuard sync failure")
	}
	if heartbeatCalls.Load() != 1 || peerCalls.Load() != 1 {
		t.Fatalf("unexpected request counts: heartbeat=%d peers=%d", heartbeatCalls.Load(), peerCalls.Load())
	}
}

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
	w := Worker{cfg: config.Config{
		RuntimeSocketPath: runtimeSocket, AllowedRuntimeBackends: []string{"native", "docker_sandbox"},
	}.WithDefaults()}
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
	operations := make(chan string, 16)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			var request runtimehelper.Request
			if json.NewDecoder(connection).Decode(&request) == nil {
				operations <- request.Operation
				_ = json.NewEncoder(connection).Encode(runtimehelper.Response{
					Version: runtimehelper.ProtocolVersion, OK: true, Result: result,
				})
			}
			_ = connection.Close()
		}
	}()
	return path, operations
}

func TestWorkerPollOnceCompletesTask(t *testing.T) {
	var completed bool
	runtimeSocket, _ := startRuntimeHelperStub(t, map[string]any{"sessions": []any{}})
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
	cfg := config.Config{
		NodeID: "node_1", ServerURL: server.URL, NodeToken: "node_token",
		RuntimeSocketPath: runtimeSocket,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !completed {
		t.Fatal("expected task completion")
	}
}

func TestWorkerHandlesEgoBrowserRequestCancellationTask(t *testing.T) {
	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	broker, err := egobrowser.New(egobrowser.Config{
		Enabled: true, NodeID: "node_1", StateRoot: stateRoot,
		SocketPath: t.TempDir() + "/broker.sock",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	w := Worker{browserBroker: broker}
	result, err := w.executeKnownTask(context.Background(), api.TaskEnvelope{
		TaskType: "cancel_ego_browser_request",
		Payload: map[string]any{
			"binding_id": "binding-1", "generation": 2,
			"request_id": "request-1", "sequence": 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "cancellation_completed" || result["request_active"] != false ||
		result["server_terminal_observed"] != false {
		t.Fatalf("unexpected cancellation task result: %#v", result)
	}

	_, err = w.executeKnownTask(context.Background(), api.TaskEnvelope{
		TaskType: "cancel_ego_browser_request",
		Payload: map[string]any{
			"binding_id": "binding-1", "generation": 2,
			"request_id": "request-1", "sequence": 3, "script": "forbidden",
		},
	})
	if err == nil {
		t.Fatal("accepted cancellation task content")
	}

	failure := contentSafeTaskError(
		api.TaskEnvelope{TaskType: "cancel_ego_browser_request"},
		errors.New("sensitive-browser-content"),
	)
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"code":"EGO_BROWSER_CANCELLATION_FAILED","message":"Cancellation could not be confirmed."}` {
		t.Fatalf("unexpected cancellation failure: %s", encoded)
	}
	if strings.Contains(string(encoded), "sensitive-browser-content") {
		t.Fatal("cancellation failure leaked browser content")
	}
}

func TestWorkerPollOnceReplaysFailedTask(t *testing.T) {
	var failed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{{
						"task_id":         "task_failed",
						"node_id":         "node_1",
						"task_type":       "stop_tool_session",
						"idempotency_key": "task_failed",
						"payload":         map[string]any{},
					}},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_failed/fail":
			var body struct {
				Error map[string]any `json:"error"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Error["code"] != "NODE_TASK_FAILED" || body.Error["message"] != "runtime failed" {
				t.Fatalf("unexpected task error: %#v", body.Error)
			}
			failed = true
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
	if err := taskLedger.Save(ledger.Entry{
		TaskID: "task_failed",
		Status: "failed",
		Error: map[string]any{
			"code":    "NODE_TASK_FAILED",
			"message": "runtime failed",
		},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{NodeID: "node_1", ServerURL: server.URL, NodeToken: "node_token"}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)
	if err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !failed {
		t.Fatal("expected persisted task failure to be replayed")
	}
}

func TestWorkerPollOnceContinuesAfterTaskError(t *testing.T) {
	var secondTaskCompleted atomic.Bool
	runtimeSocket, _ := startRuntimeHelperStub(t, map[string]any{"sessions": []any{}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/node-api/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{
						{
							"task_id": "task_failing", "node_id": "node_1", "task_type": "reconcile_state",
							"idempotency_key": "task_failing", "payload": map[string]any{},
						},
						{
							"task_id": "task_succeeding", "node_id": "node_1", "task_type": "reconcile_state",
							"idempotency_key": "task_succeeding", "payload": map[string]any{},
						},
					},
				},
				"request_id": "req_test",
			})
		case "/api/v1/node-api/tasks/task_failing/start":
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
		case "/api/v1/node-api/tasks/task_succeeding/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/node-api/tasks/task_succeeding/complete":
			secondTaskCompleted.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
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
		RuntimeSocketPath: runtimeSocket,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, "node_token"), taskLedger)

	err = w.PollOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "task task_failing") {
		t.Fatalf("expected first task error, got %v", err)
	}
	if !secondTaskCompleted.Load() {
		t.Fatal("expected the second task to complete")
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
		SourcePath:            "/etc/agent-remote-node/config.json",
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
	if !strings.Contains(string(data), "/usr/local/bin/agent-remote-attach --config /etc/agent-remote-node/config.json --session sess_1 --device dev_1") {
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

func TestNativeEgoBrowserSessionCapabilityIsRuntimeScopedAndRemovedOnStop(t *testing.T) {
	runtimeSocket, operations := startRuntimeHelperStub(t, map[string]any{
		"status": "stopped", "session_id": "session_1", "runtime_backend": "native",
	})
	brokerRoot := t.TempDir()
	if err := os.Chmod(brokerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeID: "node_1", RuntimeSocketPath: runtimeSocket,
		EgoBrowserEnabled: true, EgoBrowserBrokerRoot: brokerRoot,
		EgoBrowserBrokerSocket: t.TempDir() + "/broker.sock",
		AllowedRuntimeBackends: []string{"native"},
	}.WithDefaults()
	w := New(cfg, api.Client{}, nil)
	if w.browserBroker == nil || w.brokerErr != nil {
		t.Fatalf("browser broker initialization failed: %v", w.brokerErr)
	}
	defer w.browserBroker.Close()
	payload := map[string]any{
		"session_id": "session_1", "tool_type": "claude",
		"ego_browser_binding_id":   "task-controlled-binding",
		"ego_browser_broker_nonce": "task-controlled-nonce",
	}
	registration, err := w.applyEgoBrowserRuntimeContext(payload, "start_session")
	if err != nil {
		t.Fatal(err)
	}
	if registration == nil || !registration.created || registration.nonce == "" {
		t.Fatalf("missing session registration: %#v", registration)
	}
	if _, exists := payload["ego_browser_binding_id"]; exists {
		t.Fatal("task-controlled binding identity survived runtime context mapping")
	}
	if payload["ego_browser_broker_nonce"] != registration.nonce || registration.nonce == "task-controlled-nonce" {
		t.Fatal("runtime did not receive the broker-minted session capability")
	}
	if payload["ego_browser_task_space"] != "agent-remote:session_1" {
		t.Fatalf("unexpected task space: %v", payload["ego_browser_task_space"])
	}

	_, err = w.executeKnownTask(context.Background(), api.TaskEnvelope{
		TaskID: "stop_session_1", TaskType: "stop_tool_session",
		Payload: map[string]any{"session_id": "session_1", "runtime_backend": "native"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation := <-operations; operation != "stop_session" {
		t.Fatalf("unexpected helper operation %q", operation)
	}
	rotated, created, err := w.browserBroker.RegisterToolSession("session_1")
	if err != nil {
		t.Fatal(err)
	}
	if !created || rotated == registration.nonce {
		t.Fatal("session stop did not invalidate its broker capability")
	}
}

func TestDockerSessionReceivesRuntimeScopedEgoBrowserCapability(t *testing.T) {
	brokerRoot := t.TempDir()
	if err := os.Chmod(brokerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	w := New(config.Config{
		NodeID: "node_1", EgoBrowserEnabled: true, EgoBrowserBrokerRoot: brokerRoot,
		EgoBrowserBrokerSocket: t.TempDir() + "/broker.sock",
	}.WithDefaults(), api.Client{}, nil)
	if w.browserBroker == nil || w.brokerErr != nil {
		t.Fatalf("browser broker initialization failed: %v", w.brokerErr)
	}
	defer w.browserBroker.Close()
	payload := map[string]any{
		"session_id": "session_1", "tool_type": "claude",
		"ego_browser_binding_id":   "task-controlled-binding",
		"ego_browser_broker_nonce": "task-controlled-nonce",
	}
	registration, err := w.applyEgoBrowserRuntimeContext(payload, "docker_start_session")
	if err != nil {
		t.Fatal(err)
	}
	if registration == nil || !registration.created || payload["ego_browser_enabled"] != true {
		t.Fatalf("Docker session did not receive ego-browser context: registration=%#v payload=%#v", registration, payload)
	}
	if _, exists := payload["ego_browser_binding_id"]; exists {
		t.Fatal("task-controlled binding identity survived Docker context mapping")
	}
	if payload["ego_browser_broker_nonce"] != registration.nonce || registration.nonce == "task-controlled-nonce" {
		t.Fatal("Docker runtime did not receive the broker-minted session capability")
	}
	if payload["ego_browser_task_space"] != "agent-remote:session_1" {
		t.Fatalf("unexpected Docker task space: %v", payload["ego_browser_task_space"])
	}
	if nonce, created, err := w.browserBroker.RegisterToolSession("session_1"); err != nil || created || nonce != registration.nonce {
		t.Fatalf("Docker capability was not registered exactly once: nonce=%q created=%v err=%v", nonce, created, err)
	}
}

func TestTrustedRuntimeUIDAuthorizesEgoBrowserSessionPeer(t *testing.T) {
	runtimeSocket, operations := startRuntimeHelperStub(t, map[string]any{
		"status": "running", "session_id": "session_1", "runtime_backend": "native",
		"runtime_uid": os.Getuid(),
	})
	brokerRoot := t.TempDir()
	if err := os.Chmod(brokerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	socketRoot, err := os.MkdirTemp("/tmp", "ar-ego-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	w := New(config.Config{
		NodeID: "node_1", RuntimeSocketPath: runtimeSocket,
		EgoBrowserEnabled: true, EgoBrowserBrokerRoot: brokerRoot,
		EgoBrowserBrokerSocket: socketRoot + "/broker.sock",
	}.WithDefaults(), api.Client{}, nil)
	if w.browserBroker == nil || w.brokerErr != nil {
		t.Fatalf("browser broker initialization failed: %v", w.brokerErr)
	}
	defer w.browserBroker.Close()

	result, err := w.callRuntimeHelper(
		context.Background(),
		api.TaskEnvelope{TaskID: "start_session_1"},
		"start_session",
		map[string]any{"session_id": "session_1", "tool_type": "claude"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, exposed := result["runtime_uid"]; exposed {
		t.Fatal("trusted runtime UID escaped into the task result")
	}
	if operation := <-operations; operation != "start_session" {
		t.Fatalf("unexpected helper operation %q", operation)
	}
	nonce, created, err := w.browserBroker.RegisterToolSession("session_1")
	if err != nil || created {
		t.Fatalf("authorized capability was not retained: created=%v err=%v", created, err)
	}
	if err := w.browserBroker.AuthorizeToolSessionPeer("session_1", nonce, uint32(os.Getuid())); err != nil {
		t.Fatalf("trusted runtime UID was not retained: %v", err)
	}
	if err := w.browserBroker.AuthorizeToolSessionPeer("session_1", nonce, uint32(os.Getuid()+1)); !errors.Is(err, egobrowser.ErrProtocol) {
		t.Fatalf("different runtime UID replaced the trusted UID: %v", err)
	}
}

func TestFailedSessionStartRollsBackNewEgoBrowserCapability(t *testing.T) {
	brokerRoot := t.TempDir()
	if err := os.Chmod(brokerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		NodeID: "node_1", RuntimeSocketPath: t.TempDir() + "/missing.sock",
		EgoBrowserEnabled: true, EgoBrowserBrokerRoot: brokerRoot,
		EgoBrowserBrokerSocket: t.TempDir() + "/broker.sock",
	}.WithDefaults()
	w := New(cfg, api.Client{}, nil)
	if w.browserBroker == nil || w.brokerErr != nil {
		t.Fatalf("browser broker initialization failed: %v", w.brokerErr)
	}
	defer w.browserBroker.Close()
	_, err := w.callRuntimeHelper(context.Background(), api.TaskEnvelope{TaskID: "start_session_1"}, "start_session", map[string]any{
		"session_id": "session_1", "tool_type": "claude",
	})
	if err == nil {
		t.Fatal("missing runtime helper unexpectedly accepted session start")
	}
	_, created, err := w.browserBroker.RegisterToolSession("session_1")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("failed session start retained its newly minted capability")
	}
}

func TestTakeRuntimeUIDRejectsRoot(t *testing.T) {
	result := map[string]any{"runtime_uid": 0}
	if _, err := takeRuntimeUID(result); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root runtime UID was accepted: %v", err)
	}
	if _, exposed := result["runtime_uid"]; exposed {
		t.Fatal("rejected trusted runtime UID escaped into the task result")
	}
}
