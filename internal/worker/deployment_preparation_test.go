package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type deploymentClientStub struct {
	get                func(context.Context, skillmanager.SkillDeploymentIdentity, int64) (skillmanager.SkillDeployment, error)
	file               func(context.Context, skillmanager.SkillDeploymentIdentity, int64, skillmanager.Entry, io.Writer) error
	lease              func(context.Context, skillmanager.SkillDeploymentIdentity, int64) (api.SkillDeploymentLease, error)
	confirm            func(context.Context, api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error)
	inspect            func(context.Context, api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error)
	terminationGet     func(context.Context, skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error)
	terminationRequest func(context.Context, skillmanager.SkillDeploymentIdentity, api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error)
	terminationConfirm func(context.Context, api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error)
	terminationInspect func(context.Context, api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error)
}

func (s deploymentClientStub) GetSkillDeploymentTermination(c context.Context, b skillmanager.SkillDeploymentIdentity) (*api.SkillDeploymentTerminationIntent, error) {
	if s.terminationGet == nil {
		return nil, nil
	}
	return s.terminationGet(c, b)
}
func (s deploymentClientStub) RequestSkillDeploymentTermination(c context.Context, b skillmanager.SkillDeploymentIdentity, r api.SkillDeploymentTerminationRequest) (api.SkillDeploymentTerminationIntent, error) {
	if s.terminationRequest == nil {
		return api.SkillDeploymentTerminationIntent{}, errDeploymentPending
	}
	return s.terminationRequest(c, b, r)
}
func (s deploymentClientStub) ConfirmSkillDeploymentTermination(c context.Context, r api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error) {
	return s.terminationConfirm(c, r)
}
func (s deploymentClientStub) InspectSkillDeploymentTermination(c context.Context, r api.SkillDeploymentTerminatedResult) (api.SkillDeploymentTerminationObservation, error) {
	return s.terminationInspect(c, r)
}

func (s deploymentClientStub) GetSkillDeployment(c context.Context, b skillmanager.SkillDeploymentIdentity, a int64) (skillmanager.SkillDeployment, error) {
	return s.get(c, b, a)
}
func (s deploymentClientStub) ReadSkillDeploymentFile(c context.Context, b skillmanager.SkillDeploymentIdentity, a int64, e skillmanager.Entry, w io.Writer) error {
	return s.file(c, b, a, e, w)
}
func (s deploymentClientStub) RenewSkillDeploymentLease(c context.Context, b skillmanager.SkillDeploymentIdentity, a int64) (api.SkillDeploymentLease, error) {
	return s.lease(c, b, a)
}
func (s deploymentClientStub) ConfirmSkillDeployment(c context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
	return s.confirm(c, r)
}
func (s deploymentClientStub) InspectSkillDeployment(c context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
	return s.inspect(c, r)
}

type deploymentHelperStub func(context.Context, string, skillmanager.SkillDeployment, runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error)

func (s deploymentHelperStub) DrainSkillDeployment(context.Context, string, skillmanager.SkillDeploymentIdentity) (skillmanager.DeploymentDrain, error) {
	return skillmanager.DeploymentDrain{}, errDeploymentPending
}

func (s deploymentHelperStub) PrepareSkillDeployment(c context.Context, id string, d skillmanager.SkillDeployment, download runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
	return s(c, id, d, download)
}

func workerDeploymentFixture(t *testing.T) (skillmanager.SkillDeployment, skillmanager.DeploymentPreparation, deploymentJournal) {
	t.Helper()
	data, err := os.ReadFile("../skillmanager/testdata/deployment-v1.json")
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile("testdata/deployment-v1.json")
	}
	if err != nil {
		t.Fatal(err)
	}
	var input skillmanager.SkillDeployment
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	digest, err := input.InputDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := skillmanager.DeploymentPreparation{Version: 1, Binding: input.SkillDeploymentIdentity, InputDigest: digest, DirectoryEpoch: input.DirectoryEpoch, Generation: input.Plan.Generation, HelperReceiptID: input.NodeID}
	store, err := ledger.Open(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	return input, receipt, deploymentJournal{store}
}

func deploymentAccepted(result api.SkillDeploymentPreparedResult) api.SkillDeploymentResultObservation {
	return api.SkillDeploymentResultObservation{Result: result, Accepted: true, CurrentLeaseAttempt: result.LeaseAttempt, TaskStatus: "succeeded"}
}

func deploymentWorkerClient(input skillmanager.SkillDeployment) deploymentClientStub {
	return deploymentClientStub{
		get: func(context.Context, skillmanager.SkillDeploymentIdentity, int64) (skillmanager.SkillDeployment, error) {
			return input, nil
		},
		file: func(_ context.Context, _ skillmanager.SkillDeploymentIdentity, _ int64, _ skillmanager.Entry, w io.Writer) error {
			_, err := io.WriteString(w, "---\nname: learning\ndescription: example\n---\n")
			return err
		},
		lease: func(_ context.Context, b skillmanager.SkillDeploymentIdentity, a int64) (api.SkillDeploymentLease, error) {
			now := time.Now()
			return api.SkillDeploymentLease{SkillDeploymentIdentity: b, LeaseAttempt: a, ServerTime: now, LeaseUntil: now.Add(time.Second), RenewAfterMilliseconds: 10}, nil
		},
		confirm: func(_ context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
			return deploymentAccepted(r), nil
		},
		inspect: func(_ context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
			return deploymentAccepted(r), nil
		},
	}
}

func TestDeploymentWorkerPersistsBeforeConfirmationAndRecoversLostResponse(t *testing.T) {
	input, receipt, journal := workerDeploymentFixture(t)
	client := deploymentWorkerClient(input)
	taskID := "prepare_account_skills:" + input.AttemptID
	helperCalls := 0
	helper := deploymentHelperStub(func(ctx context.Context, id string, d skillmanager.SkillDeployment, download runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
		helperCalls++
		if id != "deployment-prepare:"+input.TaskID || d.SkillDeploymentIdentity != input.SkillDeploymentIdentity {
			t.Error("Helper received changed authority")
		}
		if err := download(ctx, input.Manifest.Entries[1], io.Discard); err != nil {
			return skillmanager.DeploymentPreparation{}, err
		}
		return receipt, nil
	})
	client.confirm = func(_ context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
		pending, err := journal.load(taskID)
		if err != nil || pending == nil || pending.entry.Status != deploymentPreparedPending || pending.record.Result != r {
			t.Error("confirmation preceded durable original proposal", err)
		}
		return api.SkillDeploymentResultObservation{}, errors.New("response lost after commit")
	}
	if err := runDeployment(context.Background(), client, helper, journal, taskID, input.SkillDeploymentIdentity, 1); err == nil {
		t.Fatal("uncertain response reported success")
	}
	if err := inspectPendingDeployments(context.Background(), client, journal, input.NodeID); err != nil {
		t.Fatal(err)
	}
	confirmed, err := journal.load(taskID)
	if err != nil || confirmed.entry.Status != deploymentPreparedConfirmed || confirmed.record.Result.Preparation != receipt {
		t.Fatal("recovery changed original integer receipt", err)
	}
	if err := runDeployment(context.Background(), client, helper, journal, taskID, input.SkillDeploymentIdentity, 1); err != nil || helperCalls != 1 {
		t.Fatal("accepted replay reran preparation", err, helperCalls)
	}
}

func TestDeploymentWorkerLeaseLossCancelsHelperAndSavesOnlyRevocationRequest(t *testing.T) {
	input, _, journal := workerDeploymentFixture(t)
	client := deploymentWorkerClient(input)
	original := client.lease
	var calls atomic.Int32
	client.lease = func(ctx context.Context, b skillmanager.SkillDeploymentIdentity, a int64) (api.SkillDeploymentLease, error) {
		if calls.Add(1) > 1 {
			return api.SkillDeploymentLease{}, errors.New("revoked")
		}
		return original(ctx, b, a)
	}
	helper := deploymentHelperStub(func(ctx context.Context, _ string, _ skillmanager.SkillDeployment, _ runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
		<-ctx.Done()
		return skillmanager.DeploymentPreparation{}, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	taskID := "prepare_account_skills:" + input.AttemptID
	if err := runDeployment(ctx, client, helper, journal, taskID, input.SkillDeploymentIdentity, 1); err == nil {
		t.Fatal("lease loss did not cancel dependent work", err)
	}
	entry, exists, err := journal.ledger.Get(taskID)
	if err != nil || !exists || entry.Status != deploymentTerminationRequested {
		t.Fatal("lease loss lacks durable nonterminal revocation request", err)
	}
}

func TestDeploymentWorkerCommittedReceiptWinsRenewalFailure(t *testing.T) {
	input, receipt, journal := workerDeploymentFixture(t)
	client := deploymentWorkerClient(input)
	original := client.lease
	var calls atomic.Int32
	confirming := make(chan struct{})
	client.lease = func(ctx context.Context, b skillmanager.SkillDeploymentIdentity, a int64) (api.SkillDeploymentLease, error) {
		if calls.Add(1) == 1 {
			return original(ctx, b, a)
		}
		select {
		case <-confirming:
		case <-ctx.Done():
		}
		return api.SkillDeploymentLease{}, errors.New("already terminal")
	}
	client.confirm = func(ctx context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
		close(confirming)
		<-ctx.Done()
		return deploymentAccepted(r), nil
	}
	helper := deploymentHelperStub(func(context.Context, string, skillmanager.SkillDeployment, runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
		return receipt, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runDeployment(ctx, client, helper, journal, "prepare_account_skills:"+input.AttemptID, input.SkillDeploymentIdentity, 1); err != nil {
		t.Fatal("committed result was downgraded", err)
	}
}

func TestDeploymentWorkerReissueRequiresExactUnacceptedObservation(t *testing.T) {
	input, receipt, journal := workerDeploymentFixture(t)
	taskID := "prepare_account_skills:" + input.AttemptID
	first := api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: receipt}
	old, err := journal.propose(taskID, first, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := deploymentWorkerClient(input)
	client.inspect = func(_ context.Context, r api.SkillDeploymentPreparedResult) (api.SkillDeploymentResultObservation, error) {
		return api.SkillDeploymentResultObservation{Result: r, CurrentLeaseAttempt: 2, TaskStatus: "leased"}, nil
	}
	helper := deploymentHelperStub(func(context.Context, string, skillmanager.SkillDeployment, runtimehelper.SkillDeploymentDownloader) (skillmanager.DeploymentPreparation, error) {
		return receipt, nil
	})
	if err := runDeployment(context.Background(), client, helper, journal, taskID, input.SkillDeploymentIdentity, 2); err != nil {
		t.Fatal(err)
	}
	current, err := journal.load(taskID)
	if err != nil || current.record.Result.LeaseAttempt != 2 || current.record.Result.Preparation != receipt {
		t.Fatal("reissue changed original input", err)
	}
	if err := journal.confirm(*old, deploymentAccepted(first)); !errors.Is(err, ledger.ErrConflict) {
		t.Fatal("late original confirmation replaced newer poll", err)
	}
}
