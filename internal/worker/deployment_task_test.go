package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func deploymentEnvelope(b skillmanager.SkillDeploymentIdentity) api.TaskEnvelope {
	id := "prepare_account_skills:" + b.AttemptID
	return api.TaskEnvelope{TaskID: id, TaskRecordID: b.TaskID, TaskType: "prepare_account_skills", NodeID: b.NodeID, IdempotencyKey: id, LeaseAttempt: 1,
		Payload: map[string]any{"protocol_version": 1, "user_id": b.UserID, "operation_id": b.OperationID, "tool_account_id": b.AccountID,
			"attempt_id": b.AttemptID, "task_record_id": b.TaskID, "checkpoint_id": b.CheckpointID, "tree_digest": b.TreeDigest, "plan_digest": b.PlanDigest, "runtime_backend": b.RuntimeBackend}}
}

func TestDeploymentTaskRejectsMalformedAuthorityBeforeAnyExecution(t *testing.T) {
	input, _, _ := workerDeploymentFixture(t)
	for _, kind := range []string{"node", "record", "logical", "key", "type", "attempt", "overflow", "version", "fraction", "alias", "null", "extra", "backend", "digest"} {
		t.Run(kind, func(t *testing.T) {
			task := deploymentEnvelope(input.SkillDeploymentIdentity)
			switch kind {
			case "node":
				task.NodeID = input.UserID
			case "record":
				task.TaskRecordID = input.NodeID
			case "logical":
				task.TaskID += "-changed"
			case "key":
				task.IdempotencyKey = "changed"
			case "type":
				task.TaskType = "reconcile_state"
			case "attempt":
				task.LeaseAttempt = 0
			case "overflow":
				task.LeaseAttempt = 2147483648
			case "version":
				task.Payload["protocol_version"] = true
			case "fraction":
				task.Payload["protocol_version"] = json.Number("1.0")
			case "alias":
				task.Payload["User_ID"] = task.Payload["user_id"]
				delete(task.Payload, "user_id")
			case "null":
				task.Payload["user_id"] = nil
			case "extra":
				task.Payload["path"] = "/tmp"
			case "backend":
				task.Payload["runtime_backend"] = "docker_sandbox"
			case "digest":
				task.Payload["plan_digest"] = "invalid"
			}
			if _, err := decodeDeploymentTask(task, input.NodeID); err == nil {
				t.Fatal("invalid task authority accepted")
			}
		})
	}
	if binding, err := decodeDeploymentTask(deploymentEnvelope(input.SkillDeploymentIdentity), input.NodeID); err != nil || binding != input.SkillDeploymentIdentity {
		t.Fatal("valid original task rejected", err)
	}
}

func TestDeploymentDispatchNeverReplaysLegacyTerminalCache(t *testing.T) {
	for _, status := range []string{"failed", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			input, _, journal := workerDeploymentFixture(t)
			task := deploymentEnvelope(input.SkillDeploymentIdentity)
			if err := journal.ledger.Save(ledger.Entry{TaskID: task.TaskID, Status: status, Result: map[string]any{"status": "old"}}); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("deployment used a generic task endpoint", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			worker := Worker{cfg: config.Config{NodeID: input.NodeID, AllowedRuntimeBackends: []string{"native"}}, client: api.NewClient(server.URL, "test"), ledger: journal.ledger}
			if err := worker.executeTask(context.Background(), task); err == nil {
				t.Fatal("legacy cache became deployment authority")
			}
			entry, ok, err := journal.ledger.Get(task.TaskID)
			if err != nil || !ok || entry.Status != status {
				t.Fatal("original evidence changed", err)
			}
		})
	}
}

func TestDeploymentJournalReopensExactInt64ReceiptAndRejectsUnsafeReplacement(t *testing.T) {
	input, receipt, _ := workerDeploymentFixture(t)
	path := filepath.Join(t.TempDir(), "original.json")
	store, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := deploymentJournal{store}
	taskID := "prepare_account_skills:" + input.AttemptID
	first := api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: receipt}
	previous, err := journal.propose(taskID, first, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing_observation", "accepted", "stale_poll", "changed_input", "cancelled"} {
		next := first
		next.LeaseAttempt = 2
		observation := &api.SkillDeploymentResultObservation{Result: first, CurrentLeaseAttempt: 2, TaskStatus: "leased"}
		switch kind {
		case "missing_observation":
			observation = nil
		case "accepted":
			observation.Accepted = true
		case "stale_poll":
			observation.CurrentLeaseAttempt = 1
		case "changed_input":
			next.Preparation.HelperReceiptID = input.TaskID
		case "cancelled":
			observation.TaskStatus = "cancelled"
		}
		if _, err := journal.propose(taskID, next, previous, observation); err == nil {
			t.Fatal("unsafe proposal replacement accepted", kind)
		}
	}
	reopened, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := (deploymentJournal{reopened}).load(taskID)
	if err != nil || entry.record.Result != first || entry.record.Result.Preparation.Generation != 9007199254740993 || entry.record.Result.Preparation.DirectoryEpoch != 9007199254740995 {
		t.Fatal("restart rounded original receipt", err)
	}
}
