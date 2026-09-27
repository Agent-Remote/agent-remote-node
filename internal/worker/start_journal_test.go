package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

type startConfirmationFixture struct {
	confirm func() error
	observe api.ManagedStartObservation
	sent    int
	reads   int
}

func (f *startConfirmationFixture) ConfirmManagedSessionStart(_ context.Context, _ string, _ api.SkillSnapshotIdentity, _ int64, result api.ManagedSessionStartResult) (api.ManagedSessionStartResult, error) {
	f.sent++
	if f.confirm != nil {
		if err := f.confirm(); err != nil {
			return api.ManagedSessionStartResult{}, err
		}
	}
	return result, nil
}

func (f *startConfirmationFixture) InspectManagedSessionStart(context.Context, string, api.SkillSnapshotIdentity, int64, api.ManagedSessionStartResult) (api.ManagedStartObservation, error) {
	f.reads++
	return f.observe, nil
}

func managedJournalFixture(t *testing.T) (managedStartJournal, string, api.SkillSnapshotIdentity, api.ManagedSessionStartResult) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.json")
	store, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	binding := newSnapshotPreparationFixture(t).snapshot.SkillSnapshotIdentity
	outcome, err := api.NewManagedSessionStartResult(binding, 3, "running", "original")
	if err != nil {
		t.Fatal(err)
	}
	return managedStartJournal{ledger: store}, path, binding, outcome
}

func TestManagedStartJournalRecoversLostAcknowledgementWithoutRepublishing(t *testing.T) {
	journal, path, binding, outcome := managedJournalFixture(t)
	client := &startConfirmationFixture{observe: api.ManagedStartObservation{Accepted: true, Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "succeeded"}}
	client.confirm = func() error {
		persisted, err := ledger.Open(path)
		if err != nil {
			t.Fatal("request preceded durable intent", err)
		}
		entry, err := (managedStartJournal{ledger: persisted}).load("original")
		if err != nil || entry == nil || entry.record.Outcome != outcome || entry.entry.Status != managedStartPending {
			t.Fatal("request changed original pending outcome", err)
		}
		return errors.New("Server committed; acknowledgement lost")
	}
	if committed, err := confirmManagedStart(context.Background(), client, journal, "original", binding, outcome, nil, nil); committed || err == nil {
		t.Fatal("unknown network result became accepted")
	}
	reopened, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	journal.ledger = reopened
	if err := inspectPendingManagedStarts(context.Background(), client, journal, binding.NodeID); err != nil {
		t.Fatal(err)
	}
	entry, err := journal.load("original")
	if err != nil || entry.entry.Status != managedStartConfirmed || client.sent != 1 || client.reads != 1 {
		t.Fatal("background recovery repeated execution or changed its outcome", err)
	}
	if err := inspectPendingManagedStarts(context.Background(), client, journal, binding.NodeID); err != nil || client.reads != 1 {
		t.Fatal("confirmed journal remained pending", err)
	}
}

func TestManagedStartJournalOnlyReplacesFencedEarlierAttempt(t *testing.T) {
	for _, proof := range []string{"missing", "same_attempt", "accepted", "foreign", "terminal", "newer"} {
		t.Run(proof, func(t *testing.T) {
			journal, _, binding, original := managedJournalFixture(t)
			previous, err := journal.propose("original", binding, original, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			stopped, err := api.NewManagedSessionStartResult(binding, 4, "stopped", "")
			if err != nil {
				t.Fatal(err)
			}
			observed := &api.ManagedStartObservation{Result: original, CurrentLeaseAttempt: 4, TaskStatus: "leased"}
			switch proof {
			case "missing":
				observed = nil
			case "same_attempt":
				observed.CurrentLeaseAttempt = 3
			case "accepted":
				observed.Accepted = true
			case "foreign":
				observed.Result.TmuxSessionName = "other"
			case "terminal":
				observed.TaskStatus = "cancelled"
			}
			next, err := journal.propose("original", binding, stopped, previous, observed)
			if proof != "newer" {
				if !errors.Is(err, ledger.ErrConflict) {
					t.Fatal("unproven earlier outcome was replaced", err)
				}
				return
			}
			if err != nil || next.record.Outcome != stopped {
				t.Fatal("fenced attempt could not recover its stopped outcome", err)
			}
			if err := journal.confirm(*previous, original); !errors.Is(err, ledger.ErrConflict) {
				t.Fatal("late observation overwrote a newer proposal", err)
			}
		})
	}
}

func TestManagedStartServerReceiptSurvivesLocalAcknowledgementFailure(t *testing.T) {
	journal, path, binding, outcome := managedJournalFixture(t)
	client := &startConfirmationFixture{confirm: func() error {
		if err := os.Rename(path, path+".pending"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return nil
	}}
	committed, err := confirmManagedStart(context.Background(), client, journal, "original", binding, outcome, nil, nil)
	if !committed || err == nil {
		t.Fatal("accepted Server receipt was lost with local write failure", err)
	}
	pending, err := journal.load("original")
	if err != nil || pending.entry.Status != managedStartPending {
		t.Fatal("failed local acknowledgement discarded recovery", err)
	}
}

func TestManagedJournalRecordsNeverEnterLegacyTaskExecution(t *testing.T) {
	journal, _, binding, outcome := managedJournalFixture(t)
	if _, err := journal.propose("original", binding, outcome, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := Worker{ledger: journal.ledger}
	if err := w.executeTask(context.Background(), api.TaskEnvelope{TaskID: "original", TaskType: "create_tool_session"}); !errors.Is(err, errManagedSessionPending) {
		t.Fatal("managed journal entered generic execution", err)
	}
}

func TestManagedStartJournalConcurrentIdenticalAcknowledgementsAreIdempotent(t *testing.T) {
	journal, path, binding, outcome := managedJournalFixture(t)
	proposal, err := journal.propose("original", binding, outcome, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		writers.Go(func() {
			<-start
			if err := journal.confirm(*proposal, outcome); err != nil {
				t.Error("identical acknowledgement conflicted", err)
			}
		})
	}
	close(start)
	writers.Wait()
	reopened, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := (managedStartJournal{ledger: reopened}).load("original")
	if err != nil || entry == nil || entry.entry.Status != managedStartConfirmed || entry.record != proposal.record {
		t.Fatal("concurrent acknowledgements changed the durable original outcome", err)
	}
}

func TestManagedStartJournalCorruptionCannotBecomeMissingOrConfirmed(t *testing.T) {
	for _, corruption := range []string{"schema", "extra", "alias", "null_binding", "binding_alias", "binding_null", "outcome_alias", "outcome_null", "error", "legacy"} {
		t.Run(corruption, func(t *testing.T) {
			journal, path, binding, outcome := managedJournalFixture(t)
			proposal, err := journal.propose("original", binding, outcome, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			entry := proposal.entry
			switch corruption {
			case "schema":
				entry.Result["schema_version"] = 2
			case "extra":
				entry.Result["path"] = "/untrusted"
			case "alias":
				entry.Result["Binding"] = entry.Result["binding"]
				delete(entry.Result, "binding")
			case "null_binding":
				entry.Result["binding"] = nil
			case "binding_alias":
				fields := entry.Result["binding"].(map[string]any)
				fields["Node_ID"] = fields["node_id"]
				delete(fields, "node_id")
			case "binding_null":
				entry.Result["binding"].(map[string]any)["node_id"] = nil
			case "outcome_alias":
				fields := entry.Result["outcome"].(map[string]any)
				fields["Status"] = fields["status"]
				delete(fields, "status")
			case "outcome_null":
				entry.Result["outcome"].(map[string]any)["lease_attempt"] = nil
			case "error":
				entry.Error = map[string]any{"code": "legacy"}
			case "legacy":
				entry.Status = "succeeded"
			}
			if err := journal.ledger.Save(entry); err != nil {
				t.Fatal(err)
			}
			reopened, err := ledger.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			journal.ledger = reopened
			if record, err := journal.load("original"); err == nil || record != nil {
				t.Fatal("corrupt journal became an absent or usable proposal", err)
			}
			client := &startConfirmationFixture{}
			if entry.Status == managedStartPending {
				if err := inspectPendingManagedStarts(context.Background(), client, journal, binding.NodeID); err == nil || client.reads != 0 || client.sent != 0 {
					t.Fatal("corrupt proposal reached receipt inspection", err)
				}
			}
		})
	}
}
