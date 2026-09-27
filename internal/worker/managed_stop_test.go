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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func stopTaskFixture(t *testing.T) (api.TaskEnvelope, *finalizationFixture) {
	t.Helper()
	f := newFinalizationFixture(t, false, "published")
	b := f.capture.Binding
	id := "stop_tool_session:" + b.SessionID
	return api.TaskEnvelope{TaskID: id, IdempotencyKey: id, NodeID: b.NodeID, TaskType: "stop_tool_session", Payload: map[string]any{
		"session_id": b.SessionID, "runtime_backend": "native", "skill_finalization": map[string]any{
			"snapshot_id": b.SnapshotID, "task_id": b.TaskID, "user_id": b.UserID, "account_id": b.AccountID}}}, f
}

func TestManagedStopRequiresOriginalFrozenCapture(t *testing.T) {
	task, f := stopTaskFixture(t)
	binding, _, err := decodeManagedStop(task, task.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	observation := runtimehelper.SkillSessionObservation{SessionID: binding.SessionID, State: "finalized", Record: &f.capture}
	if _, err := stoppedCapture(binding, observation); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []string{"running", "not_started", "snapshot", "task", "account", "node", "manifest_only"} {
		t.Run(mutate, func(t *testing.T) {
			capture := f.capture
			observed := observation
			observed.Record = &capture
			switch mutate {
			case "running", "not_started":
				observed.State = mutate
				observed.Record = nil
			case "snapshot":
				capture.Binding.SnapshotID = binding.TaskID
			case "task":
				capture.Binding.TaskID = binding.SnapshotID
			case "account":
				capture.Binding.AccountID = binding.NodeID
			case "node":
				capture.Binding.NodeID = binding.AccountID
			case "manifest_only":
				capture.ObjectsVersion = 0
			}
			if _, err := stoppedCapture(binding, observed); err == nil {
				t.Fatal("unproven saving accepted")
			}
		})
	}
}

func TestManagedStopMarkerCannotFallThroughOrCacheFailure(t *testing.T) {
	for _, change := range []string{"null", "case", "extra", "backend", "node", "session_alias", "helper_missing"} {
		t.Run(change, func(t *testing.T) {
			task, f := stopTaskFixture(t)
			switch change {
			case "null":
				task.Payload["skill_finalization"] = nil
			case "case":
				task.Payload["Skill_Finalization"] = task.Payload["skill_finalization"]
				delete(task.Payload, "skill_finalization")
			case "extra":
				task.Payload["skill_finalization"].(map[string]any)["path"] = "/other"
			case "backend":
				task.Payload["runtime_backend"] = "docker_sandbox"
			case "node":
				task.NodeID = f.capture.Binding.UserID
			case "session_alias":
				task.Payload["SESSION_ID"] = f.capture.Binding.NodeID
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/start") {
					t.Error("unexpected task mutation", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			defer server.Close()
			w := New(config.Config{NodeID: f.capture.Binding.NodeID, RuntimeSocketPath: filepath.Join(t.TempDir(), "missing"), AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "test"), f.journal.ledger)
			if !managedStopTask(task) {
				t.Fatal("marker bypass")
			}
			if err := w.executeTask(context.Background(), task); err == nil {
				t.Fatal("invalid stop succeeded")
			}
			if _, exists, err := w.ledger.Get(task.TaskID); err != nil || exists {
				t.Fatal("transient failure cached", err)
			}
		})
	}
}

type cancelledTransfer struct{ finalizationTransferClient }

func (c cancelledTransfer) ObserveSkillTermination(ctx context.Context, _ skillmanager.FinalizationRecord) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestStopSaveBoundRetainsOriginalAndBackgroundResumes(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	coordinator := newFinalizationCoordinator()
	coordinator.journal = f.journal
	w := Worker{cfg: config.Config{LedgerPath: f.path}, finalizations: coordinator}
	before := time.Now()
	err := w.attemptStoppedFinalization(context.Background(), cancelledTransfer{f.client}, f, f.capture, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(before) > time.Second {
		t.Fatal("unbounded saving", err)
	}
	saved, err := f.journal.load(f.capture)
	if err != nil || saved == nil || saved.record.Receipt != nil {
		t.Fatal("lost frozen input", err)
	}
	// Simulate restart with the same original input and no stop task redelivery.
	f.reopen()
	coordinator.journal = f.journal
	f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Record: &f.capture}}}
	journal, err := coordinator.acquire(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = recoverFinalizationPage(context.Background(), f.client, f, journal, f.capture.Binding.NodeID, "")
	coordinator.release()
	if err != nil {
		t.Fatal(err)
	}
	saved, err = f.journal.load(f.capture)
	if err != nil || saved.record.Publication == nil {
		t.Fatal("background did not finish", err)
	}
}

func TestSharedFinalizationGateIsCancellableAndKeepsOneLedger(t *testing.T) {
	coordinator := newFinalizationCoordinator()
	path := filepath.Join(t.TempDir(), "ledger")
	first, err := coordinator.acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := coordinator.acquire(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	coordinator.release()
	second, err := coordinator.acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.release()
	if first.ledger != second.ledger {
		t.Fatal("opened second mutable ledger")
	}
}

func TestManagedStopQueueFreezesBeforeTransferAndReplaysOnlyProcessReceipt(t *testing.T) {
	task, f := stopTaskFixture(t)
	f.fault = "termination"
	var completions []map[string]any
	var completionMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/node-api/tasks/") {
			if strings.HasSuffix(r.URL.Path, "/complete") {
				var request struct {
					Result map[string]any `json:"result"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				completionMu.Lock()
				completions = append(completions, request.Result)
				completionMu.Unlock()
			} else if !strings.HasSuffix(r.URL.Path, "/start") {
				t.Error("unexpected task request", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		f.serve(w, r)
	}))
	defer server.Close()
	root, err := os.MkdirTemp("/tmp", "ar-stop-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		for _, operation := range []string{"stop_session", "reconcile_skill_session"} {
			connection, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			_ = connection.SetDeadline(time.Now().Add(time.Second))
			var request runtimehelper.Request
			err = json.NewDecoder(connection).Decode(&request)
			if err == nil && request.Operation != operation {
				err = fmt.Errorf("unexpected Helper operation %s", request.Operation)
			}
			if err == nil {
				result := map[string]any{"status": "stopped"}
				if operation == "reconcile_skill_session" {
					result, err = runtimehelper.Map(runtimehelper.SkillSessionObservation{SessionID: f.capture.Binding.SessionID, State: "finalized", Record: &f.capture})
				}
				if err == nil {
					err = json.NewEncoder(connection).Encode(runtimehelper.Response{Version: runtimehelper.ProtocolVersion, OK: true, Result: result})
				}
			}
			_ = connection.Close()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	w := New(config.Config{NodeID: task.NodeID, LedgerPath: f.path, RuntimeSocketPath: socket, AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "finalization-test"), f.journal.ledger)
	w.finalizations.journal = f.journal
	if err := w.executeTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if err := w.executeTask(context.Background(), task); err != nil {
		t.Fatal("replay touched unavailable Helper", err)
	}
	completionMu.Lock()
	defer completionMu.Unlock()
	if len(completions) != 2 || !reflect.DeepEqual(completions[0], managedStopResult(f.capture)) || !reflect.DeepEqual(completions[0], completions[1]) {
		t.Fatal("changed immutable stopped receipt", completions)
	}
	record, err := f.journal.load(f.capture)
	if err != nil || record == nil || record.record.Publication != nil {
		t.Fatal("network failure discarded pending capture", err)
	}
}

func TestManagedPendingStopRequiresExactIdentityAndNeverInventsDigest(t *testing.T) {
	task, f := stopTaskFixture(t)
	binding, _, err := decodeManagedStop(task, task.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"", "snapshot", "task", "user", "account", "node", "session", "state", "code", "frozen"} {
		failure := skillmanager.CaptureFailure{Binding: f.capture.Binding, Code: "quota_exceeded"}
		observation := runtimehelper.SkillSessionObservation{SessionID: binding.SessionID, State: "capture_pending", Pending: &failure}
		switch fault {
		case "snapshot":
			failure.Binding.SnapshotID = binding.TaskID
		case "task":
			failure.Binding.TaskID = binding.SnapshotID
		case "user":
			failure.Binding.UserID = binding.AccountID
		case "account":
			failure.Binding.AccountID = binding.UserID
		case "node":
			failure.Binding.NodeID = binding.UserID
		case "session":
			failure.Binding.SessionID = binding.UserID
		case "state":
			observation.State = "running"
		case "code":
			failure.Code = "raw diagnostic"
		case "frozen":
			observation.Record = &f.capture
		}
		confirmed, err := stoppedCapturePending(binding, observation)
		if (err != nil) != (fault != "") {
			t.Fatal("unsafe pending stop", fault, err)
		}
		if err == nil {
			result := managedPendingStopResult(confirmed)
			digest, present := result["incoming_digest"]
			if len(result) != 6 || !present || digest != nil || result["skill_finalization_operation_id"] != binding.SnapshotID {
				t.Fatal("invented capture result", result)
			}
		}
	}
}

func TestManagedStopQueueConfirmsPendingAfterStopCaptureError(t *testing.T) {
	task, f := stopTaskFixture(t)
	failure := skillmanager.CaptureFailure{Binding: f.capture.Binding, Code: "quota_exceeded"}
	var completions []map[string]any
	var completionMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/node-api/tasks/") {
			if strings.HasSuffix(r.URL.Path, "/complete") {
				var request struct {
					Result map[string]any `json:"result"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				completionMu.Lock()
				completions = append(completions, request.Result)
				completionMu.Unlock()
			} else if !strings.HasSuffix(r.URL.Path, "/start") {
				t.Error("unexpected task request", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		f.serve(w, r)
	}))
	defer server.Close()
	root, err := os.MkdirTemp("/tmp", "ar-stop-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		for _, operation := range []string{"stop_session", "reconcile_skill_session"} {
			connection, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			_ = connection.SetDeadline(time.Now().Add(time.Second))
			var request runtimehelper.Request
			err = json.NewDecoder(connection).Decode(&request)
			if err == nil && request.Operation != operation {
				err = fmt.Errorf("unexpected Helper operation %s", request.Operation)
			}
			if err == nil {
				result := map[string]any{"status": "stopped"}
				if operation == "reconcile_skill_session" {
					result, err = runtimehelper.Map(runtimehelper.SkillSessionObservation{SessionID: f.capture.Binding.SessionID, State: "capture_pending", Pending: &failure})
				}
				if err == nil {
					response := runtimehelper.Response{Version: runtimehelper.ProtocolVersion, OK: true, Result: result}
					if operation == "stop_session" {
						response = runtimehelper.Response{Version: runtimehelper.ProtocolVersion, OK: false, Error: &runtimehelper.Error{Code: "FINALIZATION_PENDING", Message: "capture failed"}}
					}
					err = json.NewEncoder(connection).Encode(response)
				}
			}
			_ = connection.Close()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	w := New(config.Config{NodeID: task.NodeID, LedgerPath: f.path, RuntimeSocketPath: socket, AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "finalization-test"), f.journal.ledger)
	w.finalizations.journal = f.journal
	if err := w.executeTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if err := w.executeTask(context.Background(), task); err != nil {
		t.Fatal("replay touched unavailable Helper", err)
	}
	completionMu.Lock()
	defer completionMu.Unlock()
	if len(completions) != 2 || !reflect.DeepEqual(completions[0], managedPendingStopResult(failure)) || !reflect.DeepEqual(completions[0], completions[1]) {
		t.Fatal("changed immutable stopped receipt", completions)
	}
	record, err := f.journal.load(f.capture)
	if err != nil || record != nil {
		t.Fatal("pending stop created a frozen capture journal", err)
	}
}
