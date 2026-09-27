package worker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// TestDeploymentTerminationLiveServer exercises only an explicitly supplied disposable control plane.
func TestDeploymentTerminationLiveServer(t *testing.T) {
	path := os.Getenv("AGENT_REMOTE_TEST_DEPLOYMENT_TERMINATION_FIXTURE")
	if path == "" {
		t.Skip("requires an isolated authenticated Server termination fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read disposable fixture")
	}
	var fixture struct {
		URL              string                       `json:"url"`
		Token            string                       `json:"token"`
		Input            skillmanager.SkillDeployment `json:"input"`
		DropConfirmation bool                         `json:"drop_confirmation"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal("invalid disposable fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	control := api.NewClient(fixture.URL, fixture.Token)
	binding := fixture.Input.SkillDeploymentIdentity
	input, err := control.GetSkillDeployment(ctx, binding, 1)
	if err != nil {
		t.Fatal("original input unavailable", err)
	}
	socket, state, stop := liveDeploymentHelper(t, input)
	helper := runtimehelper.NewClient(socket)
	prepared, err := helper.PrepareSkillDeployment(ctx, "prepare-before-drain", input, func(ctx context.Context, entry skillmanager.Entry, writer io.Writer) error {
		return control.ReadSkillDeploymentFile(ctx, binding, 1, entry, writer)
	})
	if err != nil {
		t.Fatal("original preparation failed", err)
	}
	intent, err := control.RequestSkillDeploymentTermination(ctx, binding, api.SkillDeploymentTerminationRequest{LeaseAttempt: 1, ErrorCode: "TRANSFER_FAILED"})
	if err != nil {
		t.Fatal("revocation did not commit", err)
	}
	if _, err := control.GetSkillDeployment(ctx, binding, 1); err == nil {
		t.Fatal("revoked input remained authorized")
	}
	if _, err := control.ConfirmSkillDeployment(ctx, api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: prepared}); err == nil {
		t.Fatal("late success bypassed revocation")
	}
	recovered, err := control.GetSkillDeploymentTermination(ctx, binding)
	if err != nil || recovered == nil || *recovered != intent {
		t.Fatal("revocation recovery changed intent", err)
	}
	ledgerPath := filepath.Join(t.TempDir(), "tasks.json")
	local, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	task := deploymentEnvelope(binding)
	journal := deploymentJournal{local}
	proposal := api.SkillDeploymentPreparedResult{LeaseAttempt: 1, Preparation: prepared}
	if _, err := journal.propose(task.TaskID, proposal, nil, nil); err != nil {
		t.Fatal(err)
	}
	worker := Worker{cfg: config.Config{NodeID: binding.NodeID, RuntimeSocketPath: socket, AllowedRuntimeBackends: []string{"native"}}, client: control, ledger: local}
	err = worker.executeTask(ctx, task)
	if fixture.DropConfirmation {
		if err == nil {
			t.Fatal("lost confirmation returned success")
		}
	} else if err != nil {
		t.Fatal("worker termination failed", err)
	}
	entry, exists, err := local.Get(task.TaskID)
	if err != nil || !exists {
		t.Fatal("worker failed to retain original termination", err)
	}
	pending, err := decodeDeploymentTermination(entry)
	if err != nil || pending.record.Drain == nil || pending.record.Preparation == nil || *pending.record.Preparation != proposal {
		t.Fatal("worker lost original receipts", err)
	}
	drain := *pending.record.Drain
	if _, err := helper.PrepareSkillDeployment(ctx, "prepare-after-drain", input, func(context.Context, skillmanager.Entry, io.Writer) error {
		t.Error("sealed attempt fetched content")
		return nil
	}); err == nil {
		t.Fatal("drain did not fence late preparation")
	}
	stop()
	local, err = ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	worker.ledger = local
	if err := worker.recoverDeploymentConfirmations(ctx); err != nil {
		t.Fatal("restarted worker read-only recovery failed", err)
	}
	entry, _, err = local.Get(task.TaskID)
	if err != nil || entry.Status != deploymentTerminationConfirmed {
		t.Fatal("terminal receipt did not become durable", err)
	}
	if err := worker.executeTask(ctx, task); err != nil {
		t.Fatal("accepted worker replay required Helper", err)
	}
	store, err := skillmanager.OpenExistingStateStore(state)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := skillmanager.ReadDeploymentDrain(store, binding)
	if err != nil || saved != drain {
		t.Fatal("Server confirmation differs from permanent local drain", err)
	}
	original, err := skillmanager.ReadDeploymentPreparation(ctx, store, input, skillmanager.DefaultCopyPolicy())
	if err != nil || original != prepared {
		t.Fatal("drain removed or changed complete retained bytes", err)
	}
	t.Log("verified actual Server revocation, worker dispatch, root Helper drain, durable terminal confirmation and restarted read-only recovery; fixture-seeded reservation and revocation")
}
