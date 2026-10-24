package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lifecycleTestEngine(t *testing.T) Engine {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("private lifecycle storage requires Linux root")
	}
	return NewEngine(EngineConfig{StateRoot: t.TempDir(), NodeID: "node-fixture"})
}

func TestSessionStopIntentSurvivesReopenAndBackendChange(t *testing.T) {
	e := lifecycleTestEngine(t)
	intent := sessionStopIntent{Version: 1, NodeID: e.config.NodeID, SessionID: "session-fixture", Scope: "tool_session", RequestID: "stop-fixture", Operation: "stop_session", Backend: "native"}
	if err := e.publishSessionStop(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	reopened := NewEngine(e.config)
	if err := reopened.requireSessionOpen(context.Background(), intent.SessionID); !errors.Is(err, errSessionClosed) {
		t.Fatalf("admission after reopen: %v", err)
	}
	intent.Backend = "docker_sandbox"
	intent.RequestID = "second-stop"
	if err := reopened.publishSessionStop(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if err := reopened.requireSessionOpen(context.Background(), "independent-session"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLifecycleRejectsUnsafeRecord(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "corrupt", "mode", "unknown-version"} {
		t.Run(kind, func(t *testing.T) {
			e := lifecycleTestEngine(t)
			store, err := e.openSessionLifecycleStore()
			if err != nil {
				t.Fatal(err)
			}
			store.Close()
			name := filepath.Join(e.config.StateRoot, "session-lifecycle", "v1", lifecycleName("session-fixture", ".stop.json"))
			switch kind {
			case "symlink":
				err = os.Symlink("absent", name)
			case "hardlink":
				other := filepath.Join(e.config.StateRoot, "source")
				if err = os.WriteFile(other, []byte("{}"), 0600); err == nil {
					err = os.Link(other, name)
				}
			case "mode":
				err = os.WriteFile(name, []byte("{}"), 0644)
			case "unknown-version":
				err = os.WriteFile(name, []byte(`{"version":2}`), 0600)
			default:
				err = os.WriteFile(name, []byte("{"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := e.requireSessionOpen(context.Background(), "session-fixture"); err == nil {
				t.Fatal("unsafe state admitted launch")
			}
		})
	}
}

func TestSessionLifecycleLockIsCrossProcess(t *testing.T) {
	if root := os.Getenv("AGENT_REMOTE_LIFECYCLE_TEST_ROOT"); root != "" {
		e := NewEngine(EngineConfig{StateRoot: root, NodeID: "node-fixture"})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		unlock, err := e.lockSessionLifecycle(ctx, "session-fixture", "runtime")
		if err == nil {
			unlock()
			t.Fatal("child acquired held parent lock")
		}
		return
	}
	e := lifecycleTestEngine(t)
	unlock, err := e.lockSessionLifecycle(context.Background(), "session-fixture", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLifecycleLockIsCrossProcess$")
	cmd.Env = append(os.Environ(), "AGENT_REMOTE_LIFECYCLE_TEST_ROOT="+e.config.StateRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child lock probe: %v: %s", err, output)
	}
	// Stop admission must remain available while an independent process owns runtime work.
	intent := sessionStopIntent{Version: 1, NodeID: e.config.NodeID, SessionID: "session-fixture", Scope: "tool_session", RequestID: "stop-fixture", Operation: "stop_session", Backend: "native"}
	if err := e.publishSessionStop(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLifecycleStopBeforeStartAndCachedReceipt(t *testing.T) {
	e := lifecycleTestEngine(t)
	ctx := context.Background()
	stop := Request{Version: 1, RequestID: "stop-fixture", Operation: "stop_session", Payload: map[string]any{"session_id": "session-fixture", "runtime_backend": "native"}}
	if result, err := e.Execute(ctx, stop); !errors.Is(err, errSessionStopPending) || result != nil {
		t.Fatalf("missing runtime proved stopped: %v %v", result, err)
	}
	for _, operation := range []string{"start_session", "docker_start_session"} {
		request := Request{Version: 1, RequestID: operation, Operation: operation, Payload: map[string]any{"session_id": "session-fixture"}}
		if _, err := e.Execute(ctx, request); !errors.Is(err, errSessionClosed) {
			t.Fatalf("late %s admitted: %v", operation, err)
		}
	}
	for _, operation := range []string{"start_session", "docker_start_session"} {
		backend := "native"
		if operation == "docker_start_session" {
			backend = "docker_sandbox"
		}
		request := Request{Version: 1, RequestID: "cached-" + operation, Operation: operation, SessionLifecycleVersion: 1, Payload: map[string]any{"session_id": "session-fixture", "runtime_backend": backend}}
		if err := e.saveResult(request.RequestID, map[string]any{"status": "running", "session_id": "session-fixture", "runtime_backend": backend}); err != nil {
			t.Fatal(err)
		}
		response, err := e.executeSessionResponse(ctx, request)
		if err != nil || response.Result["status"] != "running" || !response.SessionExecution.ReceiptReplay || !response.SessionExecution.StopIntent || response.SessionExecution.FreshAdmission {
			t.Fatalf("historical result acquired admission: %+v %v", response, err)
		}
	}
}

func TestSessionLifecycleStopPublishesBeforeHelperAndRuntimeLocks(t *testing.T) {
	e := lifecycleTestEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	unlock, err := e.lockSessionLifecycle(ctx, "session-fixture", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	server := NewServer("", -1, os.Geteuid(), e)
	server.mu.Lock()
	client := skillPreparationTestPeer(t, server.handle)
	done := make(chan error, 1)
	go func() {
		_, err := client.CallSession(ctx, "stop-fixture", "stop_session", map[string]any{"session_id": "session-fixture", "runtime_backend": "native"})
		done <- err
	}()
	for {
		closed, err := e.sessionStopState(ctx, "session-fixture")
		if err != nil {
			server.mu.Unlock()
			t.Fatal(err)
		}
		if closed {
			break
		}
		select {
		case <-ctx.Done():
			server.mu.Unlock()
			t.Fatal("intent waited for runtime/global lock")
		case <-time.After(time.Millisecond):
		}
	}
	called := false
	err = e.submitSessionLaunch(ctx, "session-fixture", "session", func() error { called = true; return nil })
	if !errors.Is(err, errSessionClosed) || called {
		server.mu.Unlock()
		t.Fatal("in-flight preparation bypassed final admission gate", err)
	}
	unlock()
	unlock = nil
	server.mu.Unlock()
	err = <-done
	var helper *Error
	if !errors.As(err, &helper) || helper.Code != "SESSION_STOP_PENDING" {
		t.Fatal("intent alone claimed termination", err)
	}
}

func TestSessionLifecycleScopeAndProbe(t *testing.T) {
	e := lifecycleTestEngine(t)
	if !e.probeSessionLifecycle(context.Background()) {
		t.Fatal("real private storage probe failed")
	}
	directory := filepath.Join(e.config.StateRoot, "session-lifecycle", "v1")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".probe-") {
			t.Fatal("probe state leaked")
		}
	}
	changed := e
	changed.config.NodeID = "replacement-node"
	if changed.probeSessionLifecycle(context.Background()) {
		t.Fatal("changed Node adopted original private scope")
	}
	if err := changed.requireSessionOpen(context.Background(), "brand-new-session"); err == nil {
		t.Fatal("Node scope mismatch admitted a new session")
	}
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if e.probeSessionLifecycle(context.Background()) {
		t.Fatal("unsafe state advertised support")
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("probe repaired unsafe storage")
	}
}

func TestSessionLifecycleBindingAndInvalidBatchDoNotPublish(t *testing.T) {
	e := lifecycleTestEngine(t)
	spec := DockerSessionSpec{Kind: dockerSessionKindBinding, SessionID: "binding-fixture", UserID: "user-fixture", TmuxSessionName: "binding-tmux", SandboxName: "binding-sandbox", RuntimeUID: 1000, RuntimeGID: 1000}
	if err := e.saveDockerSessionSpec(spec); err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Version: 1, RequestID: "wrong-kind", Operation: "docker_stop_session", Payload: map[string]any{"session_id": spec.SessionID, "runtime_backend": "docker_sandbox"}},
		{Version: 1, RequestID: "bad-batch", Operation: "cleanup_resources", Payload: map[string]any{"runtime_backend": "native", "session_ids": []any{"valid-session", "../invalid"}}},
		{Version: 1, RequestID: "duplicate-batch", Operation: "cleanup_resources", Payload: map[string]any{"runtime_backend": "native", "session_ids": []any{"valid-session", "valid-session"}}},
	} {
		if err := e.prepareSessionStopIntents(context.Background(), request); err == nil {
			t.Fatal("invalid stop was accepted")
		}
	}
	for _, session := range []string{spec.SessionID, "valid-session"} {
		if err := e.requireSessionOpen(context.Background(), session); err != nil {
			t.Fatal("invalid request published intent", err)
		}
	}
	if _, err := e.loadDockerSessionSpec(spec.SessionID); err != nil {
		t.Fatal("tool stop changed binding", err)
	}
}

func TestSessionLifecycleRejectedSocketRequestsCannotCloseSession(t *testing.T) {
	e := lifecycleTestEngine(t)
	server := NewServer("", -1, os.Geteuid(), e)
	for _, invalid := range []string{"protocol", "lifecycle", "oversize"} {
		t.Run(invalid, func(t *testing.T) {
			client := skillPreparationTestPeer(t, server.handle)
			request := Request{Version: 1, RequestID: "stop-fixture", Operation: "stop_session", Payload: map[string]any{"session_id": "session-fixture", "runtime_backend": "native"}}
			switch invalid {
			case "protocol":
				request.Version = 2
			case "lifecycle":
				request.SessionLifecycleVersion = 2
			case "oversize":
				request.Payload["padding"] = strings.Repeat("x", maxHelperRequestBytes)
			}
			connection, err := net.Dial("unix", client.socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if err := json.NewEncoder(connection).Encode(request); err != nil {
				t.Fatal(err)
			}
			response, err := readHelperResponse(connection)
			if err != nil || response.OK {
				t.Fatal("invalid request accepted", response, err)
			}
			if err := e.requireSessionOpen(context.Background(), "session-fixture"); err != nil {
				t.Fatal("rejected request published permanent marker", err)
			}
		})
	}
}
