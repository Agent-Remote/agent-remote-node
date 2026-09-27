package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestDeploymentTerminationCorruptJournalStopsBeforeExternalWork(t *testing.T) {
	for _, kind := range []string{"null_request", "missing", "alias", "extra", "binding_alias", "null_binding", "status", "foreign_node", "foreign_task"} {
		t.Run(kind, func(t *testing.T) {
			input, _, journal := workerDeploymentFixture(t)
			intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
			pending, err := journal.beginTermination(input.SkillDeploymentIdentity, intent.Request, &intent, nil)
			if err != nil {
				t.Fatal(err)
			}
			entry := pending.entry
			switch kind {
			case "null_request":
				entry.Result["request"] = nil
			case "missing":
				delete(entry.Result, "confirmation")
			case "alias":
				entry.Result["Intent"] = entry.Result["intent"]
				delete(entry.Result, "intent")
			case "extra":
				entry.Result["path"] = "/tmp"
			case "binding_alias":
				b := entry.Result["binding"].(map[string]any)
				b["Node_ID"] = b["node_id"]
				delete(b, "node_id")
			case "null_binding":
				entry.Result["binding"] = nil
			case "status":
				entry.Status = deploymentTerminationDrained
			case "foreign_node":
				input.NodeID = input.UserID
			case "foreign_task":
				entry.Result["binding"].(map[string]any)["task_id"] = input.UserID
			}
			if err := journal.ledger.Save(entry); err != nil {
				t.Fatal(err)
			}
			client := deploymentWorkerClient(input)
			client.terminationGet = func(context.Context, skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error) {
				t.Fatal("invalid journal called Server")
				return nil, nil
			}
			helper := deploymentTerminationHelperStub{drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
				t.Fatal("invalid journal called Helper")
				return skillmanager.DeploymentDrain{}, nil
			}}
			if err := recoverDeploymentTerminations(context.Background(), client, helper, journal, input.NodeID); err == nil {
				t.Fatal("unsafe record accepted")
			}
		})
	}
}

func TestDeploymentTerminationRequiresDurableIntentBeforeHelper(t *testing.T) {
	input, _, _ := workerDeploymentFixture(t)
	root := filepath.Join(t.TempDir(), "ledger")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := ledger.Open(filepath.Join(root, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	journal := deploymentJournal{store}
	intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
	pending, err := journal.beginTermination(input.SkillDeploymentIdentity, intent.Request, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := deploymentWorkerClient(input)
	client.terminationRequest = func(context.Context, skillmanager.SkillDeploymentIdentity, api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error) {
		if err := os.Rename(root, root+"-retained"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root, []byte("blocks publication"), 0600); err != nil {
			t.Fatal(err)
		}
		return intent, nil
	}
	helper := deploymentTerminationHelperStub{drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
		t.Fatal("failed persistence called Helper")
		return skillmanager.DeploymentDrain{}, nil
	}}
	if err := resumeDeploymentTermination(context.Background(), client, helper, journal, pending); err == nil {
		t.Fatal("ignored intent persistence failure")
	}
	retained, err := ledger.Open(filepath.Join(root+"-retained", "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := retained.Get(pending.entry.TaskID)
	if err != nil || entry.Status != deploymentTerminationRequested {
		t.Fatal("failure destroyed original request", err)
	}
}

func TestDeploymentTerminationConcurrentIntentWritersKeepOneOriginal(t *testing.T) {
	input, _, journal := workerDeploymentFixture(t)
	intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
	var group sync.WaitGroup
	errorsFound := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := journal.beginTermination(input.SkillDeploymentIdentity, intent.Request, &intent, nil)
			errorsFound <- err
		}()
	}
	group.Wait()
	close(errorsFound)
	accepted := 0
	for err := range errorsFound {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ledger.ErrConflict) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatal("concurrent revocation replaced original evidence", accepted)
	}
}

func TestDeploymentTerminationNewPollRecoversOldIntentBeforeChangingRequest(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncommitted", true: "committed"}[committed], func(t *testing.T) {
			input, _, journal := workerDeploymentFixture(t)
			intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
			pending, err := journal.beginTermination(input.SkillDeploymentIdentity, intent.Request, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			client := deploymentWorkerClient(input)
			client.terminationGet = func(context.Context, skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error) {
				if committed {
					return &intent, nil
				}
				return nil, nil
			}
			client.terminationRequest = func(_ context.Context, _ skillmanager.SkillDeploymentIdentity, request api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error) {
				if committed || request.LeaseAttempt != 2 {
					t.Fatal("new poll replaced existing intent or reused stale authority")
				}
				intent.Request = request
				return intent, nil
			}
			helper := deploymentTerminationHelperStub{drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
				return skillmanager.DeploymentDrain{}, errors.New("offline")
			}}
			if err := runDeployment(context.Background(), client, helper, journal, pending.entry.TaskID, input.SkillDeploymentIdentity, 2); err == nil {
				t.Fatal("offline Helper completed termination")
			}
			entry, _, _ := journal.ledger.Get(pending.entry.TaskID)
			saved, err := decodeDeploymentTermination(entry)
			if err != nil || saved.record.Intent == nil || *saved.record.Intent != intent {
				t.Fatal("changed original committed intent", err)
			}
		})
	}
}

func TestDeploymentTerminationConfirmedPollAndReceiptRemainImmutable(t *testing.T) {
	input, _, journal := workerDeploymentFixture(t)
	intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
	drain := skillmanager.DeploymentDrain{Version: 1, Binding: input.SkillDeploymentIdentity, HelperReceiptID: input.NodeID}
	result := api.SkillDeploymentTerminatedResult{Intent: intent, Drain: drain}
	observed := terminatedObservation(result)
	record := deploymentTerminationRecord{SchemaVersion: 1, Binding: input.SkillDeploymentIdentity, Request: intent.Request, Intent: &intent, Drain: &drain, Confirmation: &observed}
	pending, err := journal.saveTermination(nil, record)
	if err != nil {
		t.Fatal(err)
	}
	changed := observed
	changed.CurrentLeaseAttempt++
	record.Confirmation = &changed
	if _, err := journal.saveTermination(&pending.entry, record); err == nil {
		t.Fatal("rewrote terminal poll")
	}
	client := deploymentWorkerClient(input)
	client.terminationInspect = func(context.Context, api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error) {
		return changed, nil
	}
	helper := deploymentTerminationHelperStub{drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
		t.Fatal("confirmed journal repeated Helper")
		return drain, nil
	}}
	if err := resumeDeploymentTermination(context.Background(), client, helper, journal, pending); err == nil {
		t.Fatal("accepted changed final poll")
	}
}

func TestDeploymentTerminationNeverFallsIntoGenericTaskReplay(t *testing.T) {
	input, _, journal := workerDeploymentFixture(t)
	intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
	pending, err := journal.beginTermination(input.SkillDeploymentIdentity, intent.Request, &intent, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("termination reached generic HTTP", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()
	worker := Worker{cfg: config.Config{NodeID: input.NodeID}, client: api.NewClient(server.URL, "test"), ledger: journal.ledger}
	task := api.TaskEnvelope{TaskID: pending.entry.TaskID, TaskType: "reconcile_state"}
	if err := worker.executeTask(context.Background(), task); err == nil {
		t.Fatal("malformed task replayed terminal journal")
	}
}
