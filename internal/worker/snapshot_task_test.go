package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func managedTaskFixture(t *testing.T) api.TaskEnvelope {
	t.Helper()
	f := newSnapshotPreparationFixture(t)
	f.session.TmuxSessionName, f.session.SandboxName = "original", "unused-native"
	payload, err := runtimehelper.Map(f.session)
	if err != nil {
		t.Fatal(err)
	}
	binding := f.snapshot.SkillSnapshotIdentity
	payload["skill_manager"] = map[string]any{"protocol_version": 1, "manifest_version": 1, "snapshot_id": binding.SnapshotID, "task_id": binding.TaskID}
	payload["git_sync_policy"] = map[string]any{"exclude_hooks": true, "exclude_locks": true, "require_clean_git_lock": true, "warn_concurrent_git": true}
	return api.TaskEnvelope{TaskID: "original-task", IdempotencyKey: "original-task", TaskRecordID: binding.TaskID, NodeID: binding.NodeID, TaskType: "create_tool_session", LeaseAttempt: 3, Payload: payload}
}

func TestManagedTaskDecodingKeepsExactPointerAndPollIdentity(t *testing.T) {
	task := managedTaskFixture(t)
	decoded, err := decodeManagedSnapshotTask(task, task.NodeID)
	if err != nil || decoded.binding.TaskID != task.TaskRecordID || decoded.binding.SessionID != decoded.session.SessionID {
		t.Fatal("valid managed task rejected", err)
	}
	if _, exists := task.Payload["skill_manager"]; !exists {
		t.Fatal("decoder changed caller payload")
	}
	for _, change := range []string{"null", "case", "missing", "pointer_bool", "pointer_fraction", "pointer_extra", "record", "attempt", "idempotency", "backend", "field_alias", "tool", "task_type", "node"} {
		t.Run(change, func(t *testing.T) {
			task := managedTaskFixture(t)
			pointer := task.Payload["skill_manager"].(map[string]any)
			node := task.NodeID
			switch change {
			case "null":
				task.Payload["skill_manager"] = nil
			case "case":
				task.Payload["Skill_Manager"] = pointer
			case "missing":
				delete(task.Payload, "skill_manager")
			case "pointer_bool":
				pointer["protocol_version"] = true
			case "pointer_fraction":
				pointer["manifest_version"] = 1.5
			case "pointer_extra":
				pointer["path"] = "/private"
			case "record":
				task.TaskRecordID = task.NodeID
			case "attempt":
				task.LeaseAttempt = 0
			case "idempotency":
				task.IdempotencyKey = "other"
			case "backend":
				task.Payload["runtime_backend"] = "docker_sandbox"
			case "field_alias":
				task.Payload["ARGV"] = []string{"other"}
			case "tool":
				task.Payload["tool_type"] = "other"
			case "task_type":
				task.TaskType = "stop_tool_session"
			case "node":
				node = task.TaskRecordID
			}
			if _, err := decodeManagedSnapshotTask(task, node); err == nil {
				t.Fatal("changed authority accepted")
			}
		})
	}
}

func TestManagedQueueDispatchRenewsBeforeHelperAndNeverCachesLeaseFailure(t *testing.T) {
	task := managedTaskFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		expected := "/api/v1/node/skill-snapshots/55555555-5555-4555-8555-555555555555/lease"
		if r.Method != http.MethodPost || r.URL.Path != expected || r.URL.Query().Get("task_id") != task.TaskRecordID {
			t.Error("managed dispatch entered legacy completion or execution", r.URL.Path)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	journal, _, _, _ := managedJournalFixture(t)
	w := New(config.Config{NodeID: task.NodeID, AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "dispatch-test"), journal.ledger)
	for range 2 {
		if err := w.executeTask(context.Background(), task); !errors.Is(err, errManagedSessionPending) {
			t.Fatal("lease loss became terminal", err)
		}
	}
	if _, exists, err := journal.ledger.Get(task.TaskID); err != nil || exists || requests.Load() != 2 {
		t.Fatal("lease failure was cached or retried implicitly", err)
	}
	w.cfg.AllowedRuntimeBackends = nil
	if err := w.executeTask(context.Background(), task); !errors.Is(err, errManagedSessionPending) || requests.Load() != 2 {
		t.Fatal("disabled backend reached managed startup", err)
	}
}

func TestWorkerCanConfirmPendingOutcomeWithoutTaskRedelivery(t *testing.T) {
	journal, _, binding, outcome := managedJournalFixture(t)
	if _, err := journal.propose("original", binding, outcome, nil, nil); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/node-api/tasks/original/managed-start-result/inspect" {
			t.Error("confirmation recovery polled or executed a task", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1, "status": "completed", "committed": true,
			"data": api.ManagedStartObservation{Result: outcome, Accepted: true, CurrentLeaseAttempt: 3, TaskStatus: "succeeded"},
		})
	}))
	defer server.Close()
	w := Worker{cfg: config.Config{NodeID: binding.NodeID}, client: api.NewClient(server.URL, "inspection-test"), ledger: journal.ledger}
	if err := w.recoverManagedStartConfirmations(context.Background()); err != nil {
		t.Fatal(err)
	}
	entry, err := journal.load("original")
	if err != nil || entry == nil || entry.entry.Status != managedStartConfirmed {
		t.Fatal("independent recovery did not retain exact acknowledgement", err)
	}
}

func TestManagedQueueHistoricalReceiptReleasesOnlyNewPeerRegistration(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh_broker", true: "original_peer"}[existing], func(t *testing.T) {
			w, session, grants := managedPeerFixture(t)
			w.managedAdmissions = newManagedRuntimeAdmissions()
			task := managedTaskFixture(t)
			input, err := decodeManagedSnapshotTask(task, task.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			journal, _, _, _ := managedJournalFixture(t)
			outcome, err := api.NewManagedSessionStartResult(input.binding, task.LeaseAttempt, "running", input.session.TmuxSessionName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := journal.propose(task.TaskID, input.binding, outcome, nil, nil); err != nil {
				t.Fatal(err)
			}
			var nonce string
			if existing {
				peer, err := w.prepareManagedSessionPeer(session)
				if err != nil {
					t.Fatal(err)
				}
				if err := peer.authorize(context.Background(), 12345); err != nil {
					t.Fatal(err)
				}
				nonce = peer.session.EgoBrowserBrokerNonce
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node-api/tasks/original-task/managed-start-result/inspect" {
					t.Error("historical acceptance attempted runtime execution or publication", r.URL.Path)
				}
				_ = json.NewEncoder(rw).Encode(map[string]any{
					"schema_version": 1, "status": "completed", "committed": true,
					"data": api.ManagedStartObservation{Result: outcome, Accepted: true, CurrentLeaseAttempt: task.LeaseAttempt, TaskStatus: "succeeded"},
				})
			}))
			defer server.Close()
			w.cfg.NodeID, w.cfg.AllowedRuntimeBackends = task.NodeID, []string{"native"}
			w.client, w.ledger = api.NewClient(server.URL, "historical-test"), journal.ledger
			if err := w.executeTask(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatal("historical acceptance caused extra control-plane requests")
			}
			if existing {
				if grants.Load() != 1 || w.browserBroker.VerifyToolSessionPeer(session.SessionID, nonce, 12345) != nil {
					t.Fatal("historical inspection replaced or revoked the original peer")
				}
			} else {
				if _, created, err := w.browserBroker.RegisterToolSession(session.SessionID); err != nil || !created || grants.Load() != 0 {
					t.Fatal("historical acceptance retained or granted a replacement peer", err)
				}
			}
		})
	}
}

func TestWorkerRetiresCancelledProposalWithoutTaskRedelivery(t *testing.T) {
	task := managedTaskFixture(t)
	journal, path, binding, outcome := managedJournalFixture(t)
	if _, err := journal.propose(task.TaskID, binding, outcome, nil, nil); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "cancelled"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node-api/tasks/original-task/managed-start-result/inspect" {
			t.Error("retirement invoked another operation", r.URL.Path)
		}
		var proposed api.ManagedSessionStartResult
		if err := json.NewDecoder(r.Body).Decode(&proposed); err != nil || proposed != outcome {
			t.Error("inspection changed original proposal", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "unconfirmed", "committed": false, "data": observed})
	}))
	defer server.Close()
	w := New(config.Config{NodeID: binding.NodeID, AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "inspection-test"), journal.ledger)
	if err := w.recoverManagedStartConfirmations(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.ledger = reopened
	// No admission registry or Helper is available after restart; the retired task needs neither.
	w.managedAdmissions = nil
	if err := w.recoverManagedStartConfirmations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.executeTask(context.Background(), task); !errors.Is(err, errManagedSessionPending) {
		t.Fatal("retired task resumed runtime work", err)
	}
	if requests.Load() != 1 {
		t.Fatal("retired record repeated network work")
	}
	saved, err := (managedStartJournal{ledger: reopened}).load(task.TaskID)
	if err != nil || saved.entry.Status != managedStartRetired || saved.retirement != observed {
		t.Fatal("restart lost cancelled proposal", err)
	}
}
