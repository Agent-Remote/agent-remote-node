package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type deploymentTerminationHelperStub struct {
	deploymentHelperStub
	drain func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error)
}

func (h deploymentTerminationHelperStub) DrainSkillDeployment(c context.Context, id string, b skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
	return h.drain(c, id, b)
}

func workerTerminationIntent(binding skillmanager.SkillDeploymentIdentity, poll int64, code string) api.SkillDeploymentTerminationIntent {
	intent := api.SkillDeploymentTerminationIntent{Version: 1, IntentID: binding.NodeID, Binding: binding,
		Request: api.SkillDeploymentTerminationRequest{LeaseAttempt: poll, ErrorCode: code}, Outcome: "failed", ErrorCode: code, Retryable: true}
	if code == "OPERATION_SUPERSEDED" {
		intent.Outcome, intent.Retryable = "superseded", false
	}
	return intent
}

func terminatedObservation(r api.SkillDeploymentTerminatedResult) api.SkillDeploymentTerminationObservation {
	status := "failed"
	if r.Intent.Outcome == "superseded" {
		status = "cancelled"
	}
	return api.SkillDeploymentTerminationObservation{Result: r, Accepted: true, CurrentLeaseAttempt: r.Intent.Request.LeaseAttempt + 1, TaskStatus: status}
}

func TestDeploymentTerminationRestartAtEachLostResponse(t *testing.T) {
	for _, lost := range []string{"none", "revocation", "drain", "confirmation"} {
		t.Run(lost, func(t *testing.T) {
			input, _, _ := workerDeploymentFixture(t)
			binding := input.SkillDeploymentIdentity
			taskID := "prepare_account_skills:" + input.AttemptID
			path := filepath.Join(t.TempDir(), "tasks.json")
			store, err := ledger.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			journal := deploymentJournal{store}
			intent := workerTerminationIntent(binding, 1, "OPERATION_SUPERSEDED")
			drain := skillmanager.DeploymentDrain{Version: 1, Binding: binding, HelperReceiptID: input.TaskID}
			var revoked, drained, confirmed bool
			var requests, drains, confirmations, preparations int
			assertSaved := func(status string) deploymentTerminationRecord {
				t.Helper()
				entry, exists, err := journal.ledger.Get(taskID)
				if err != nil || !exists || entry.Status != status {
					t.Fatalf("side effect preceded durable %s: %v", status, err)
				}
				decoded, err := decodeDeploymentTermination(entry)
				if err != nil {
					t.Fatal(err)
				}
				return decoded.record
			}
			client := deploymentWorkerClient(input)
			client.lease = func(context.Context, skillmanager.SkillDeploymentIdentity, int64) (api.SkillDeploymentLease, error) {
				return api.SkillDeploymentLease{}, &api.HTTPError{StatusCode: 409, Code: "OPERATION_SUPERSEDED"}
			}
			client.terminationGet = func(context.Context, skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error) {
				if revoked {
					return &intent, nil
				}
				return nil, nil
			}
			client.terminationRequest = func(_ context.Context, b skillmanager.SkillDeploymentIdentity, r api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error) {
				requests++
				if saved := assertSaved(deploymentTerminationRequested); saved.Binding != b || saved.Request != r || r != intent.Request {
					t.Fatal("request identity changed")
				}
				revoked = true
				if lost == "revocation" && requests == 1 {
					return api.SkillDeploymentTerminationIntent{}, errors.New("lost revocation ACK")
				}
				return intent, nil
			}
			client.terminationInspect = func(_ context.Context, r api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error) {
				if confirmed {
					return terminatedObservation(r), nil
				}
				return api.SkillDeploymentTerminationObservation{Result: r, CurrentLeaseAttempt: 1, TaskStatus: "expired"}, nil
			}
			client.terminationConfirm = func(_ context.Context, r api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error) {
				confirmations++
				if saved := assertSaved(deploymentTerminationDrained); !drained || saved.result() != r {
					t.Fatal("confirmation lacks saved exact drain")
				}
				confirmed = true
				if lost == "confirmation" && confirmations == 1 {
					return api.SkillDeploymentTerminationObservation{}, errors.New("lost terminal ACK")
				}
				return terminatedObservation(r), nil
			}
			helper := deploymentTerminationHelperStub{
				deploymentHelperStub: func(context.Context, string, skillmanager.SkillDeployment, runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
					preparations++
					return skillmanager.DeploymentPreparation{}, errors.New("unexpected preparation")
				},
				drain: func(_ context.Context, id string, b skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
					drains++
					if saved := assertSaved(deploymentTerminationRevoked); !revoked || saved.Intent == nil || *saved.Intent != intent || b != binding || id != "deployment-drain:"+binding.TaskID {
						t.Fatal("drain preceded exact durable intent")
					}
					drained = true
					if lost == "drain" && drains == 1 {
						return skillmanager.DeploymentDrain{}, errors.New("lost Helper ACK")
					}
					return drain, nil
				},
			}
			err = runDeployment(context.Background(), client, helper, journal, taskID, binding, 1)
			if (err != nil) != (lost != "none") {
				t.Fatal("incorrect initial result", err)
			}
			store, err = ledger.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			journal = deploymentJournal{store}
			if err := recoverDeploymentTerminations(context.Background(), client, helper, journal, input.NodeID); err != nil {
				t.Fatal(err)
			}
			saved := assertSaved(deploymentTerminationConfirmed)
			if saved.Intent == nil || *saved.Intent != intent || *saved.Drain != drain || *saved.Confirmation != terminatedObservation(saved.result()) {
				t.Fatal("restart changed immutable evidence")
			}
			if err := runDeployment(context.Background(), client, helper, journal, taskID, binding, 9); err != nil {
				t.Fatal("accepted replay required a lease", err)
			}
			expectedDrains := 1
			if lost == "drain" {
				expectedDrains = 2
			}
			if requests != 1 || drains != expectedDrains || confirmations != 1 || preparations != 0 {
				t.Fatal("replayed a completed side effect", requests, drains, confirmations, preparations)
			}
		})
	}
}

func TestDeploymentRevocationRetainsPendingSuccessAndBlocksStaleWriters(t *testing.T) {
	input, receipt, journal := workerDeploymentFixture(t)
	taskID := "prepare_account_skills:" + input.AttemptID
	proposal := api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: receipt}
	previous, err := journal.propose(taskID, proposal, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
	client := deploymentWorkerClient(input)
	client.terminationGet = func(context.Context, skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error) {
		return &intent, nil
	}
	client.inspect = func(context.Context, api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
		t.Fatal("revocation attempted success inspection")
		return api.SkillDeploymentResultObservation{}, nil
	}
	if err := inspectPendingDeployments(context.Background(), client, journal, input.NodeID); err != nil {
		t.Fatal(err)
	}
	entry, _, _ := journal.ledger.Get(taskID)
	revoked, err := decodeDeploymentTermination(entry)
	if err != nil || revoked.record.Preparation == nil || *revoked.record.Preparation != proposal || revoked.record.Preparation.Preparation.Generation != 9007199254740993 {
		t.Fatal("revocation discarded or rounded original proposal", err)
	}
	if err := journal.confirm(*previous, deploymentAccepted(proposal)); err == nil {
		t.Fatal("stale success replaced revocation")
	}
	changed := revoked.record
	changed.Intent = new(api.SkillDeploymentTerminationIntent)
	*changed.Intent = intent
	changed.Intent.IntentID = input.TaskID
	if _, err := journal.saveTermination(&revoked.entry, changed); err == nil {
		t.Fatal("replaced committed intent")
	}
	changed = revoked.record
	changed.Preparation = nil
	if _, err := journal.saveTermination(&revoked.entry, changed); err == nil {
		t.Fatal("removed original success evidence")
	}
}

func TestDeploymentRequestedRevocationRecoversCompetingCommittedSuccess(t *testing.T) {
	input, receipt, journal := workerDeploymentFixture(t)
	taskID := "prepare_account_skills:" + input.AttemptID
	proposal := api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: receipt}
	previous, err := journal.propose(taskID, proposal, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := api.SkillDeploymentTerminationRequest{LeaseAttempt: 1, ErrorCode: "DEPLOYMENT_INTERRUPTED"}
	requested, err := journal.beginTermination(input.SkillDeploymentIdentity, request, nil, previous)
	if err != nil {
		t.Fatal(err)
	}
	client := deploymentWorkerClient(input)
	client.terminationRequest = func(context.Context, skillmanager.SkillDeploymentIdentity, api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error) {
		t.Fatal("revoked committed success")
		return api.SkillDeploymentTerminationIntent{}, nil
	}
	helper := deploymentTerminationHelperStub{drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
		t.Fatal("drained committed success")
		return skillmanager.DeploymentDrain{}, nil
	}}
	if err := resumeDeploymentTermination(context.Background(), client, helper, journal, requested); err != nil {
		t.Fatal(err)
	}
	confirmed, err := journal.load(taskID)
	if err != nil || confirmed.entry.Status != deploymentPreparedConfirmed || confirmed.record.Result != proposal {
		t.Fatal("lost exact success recovery", err)
	}
	intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "DEPLOYMENT_INTERRUPTED")
	if _, err := journal.beginTermination(input.SkillDeploymentIdentity, request, &intent, confirmed); err == nil {
		t.Fatal("downgraded confirmed success")
	}
	if _, err := journal.beginTermination(input.SkillDeploymentIdentity, request, &intent, previous); err == nil {
		t.Fatal("stale writer downgraded confirmed success")
	}
}

func TestDeploymentTerminationRejectsInvalidOrForeignDrain(t *testing.T) {
	for _, kind := range []string{"missing", "foreign", "changed_intent"} {
		t.Run(kind, func(t *testing.T) {
			input, _, journal := workerDeploymentFixture(t)
			intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "TRANSFER_FAILED")
			pending, err := journal.beginTermination(input.SkillDeploymentIdentity, intent.Request, &intent, nil)
			if err != nil {
				t.Fatal(err)
			}
			client := deploymentWorkerClient(input)
			client.terminationInspect = func(context.Context, api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error) {
				t.Fatal("invalid drain reached Server")
				return api.SkillDeploymentTerminationObservation{}, nil
			}
			helper := deploymentTerminationHelperStub{drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
				if kind == "missing" {
					return skillmanager.DeploymentDrain{}, nil
				}
				drain := skillmanager.DeploymentDrain{Version: 1, Binding: input.SkillDeploymentIdentity, HelperReceiptID: input.NodeID}
				drain.Binding.TaskID = input.UserID
				return drain, nil
			}}
			if kind == "changed_intent" {
				changed := pending.record
				changed.Intent = nil
				if _, err := journal.saveTermination(&pending.entry, changed); err == nil {
					t.Fatal("reopened preparation authority")
				}
				return
			}
			if err := resumeDeploymentTermination(context.Background(), client, helper, journal, pending); err == nil {
				t.Fatal("invalid drain accepted")
			}
			entry, _, _ := journal.ledger.Get(pending.entry.TaskID)
			if entry.Status != deploymentTerminationRevoked {
				t.Fatal("invalid drain advanced local state")
			}
		})
	}
}

func TestDeploymentPendingSuccessCanRevokeAfterDefiniteSupersession(t *testing.T) {
	for _, failurePhase := range []string{"renewal", "confirmation"} {
		t.Run(failurePhase, func(t *testing.T) {
			input, receipt, journal := workerDeploymentFixture(t)
			taskID := "prepare_account_skills:" + input.AttemptID
			proposal := api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: receipt}
			if failurePhase == "renewal" {
				if _, err := journal.propose(taskID, proposal, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			client := deploymentWorkerClient(input)
			client.inspect = func(_ context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
				return api.SkillDeploymentResultObservation{Result: r, CurrentLeaseAttempt: 1, TaskStatus: "leased"}, nil
			}
			if failurePhase == "renewal" {
				client.lease = func(context.Context, skillmanager.SkillDeploymentIdentity, int64) (api.SkillDeploymentLease, error) {
					return api.SkillDeploymentLease{}, &api.HTTPError{StatusCode: 409, Code: "OPERATION_SUPERSEDED"}
				}
			} else {
				client.confirm = func(context.Context, api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
					return api.SkillDeploymentResultObservation{}, &api.HTTPError{StatusCode: 409, Code: "OPERATION_SUPERSEDED"}
				}
			}
			intent := workerTerminationIntent(input.SkillDeploymentIdentity, 1, "OPERATION_SUPERSEDED")
			client.terminationRequest = func(context.Context, skillmanager.SkillDeploymentIdentity, api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error) {
				return intent, nil
			}
			helper := deploymentTerminationHelperStub{
				deploymentHelperStub: func(context.Context, string, skillmanager.SkillDeployment, runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
					return receipt, nil
				},
				drain: func(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
					return skillmanager.DeploymentDrain{}, errors.New("offline")
				},
			}
			if err := runDeployment(context.Background(), client, helper, journal, taskID, input.SkillDeploymentIdentity, 1); err == nil {
				t.Fatal("offline Helper confirmed termination")
			}
			entry, _, err := journal.ledger.Get(taskID)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := decodeDeploymentTermination(entry)
			if err != nil || pending.entry.Status != deploymentTerminationRevoked || pending.record.Preparation == nil || *pending.record.Preparation != proposal {
				t.Fatal("pending success blocked definite supersession or lost original evidence", err)
			}
		})
	}
}
