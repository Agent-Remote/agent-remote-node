package worker

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

func TestManagedStartCancellationRetiresOnlyExactUnacceptedProposal(t *testing.T) {
	for _, state := range []string{"cancelled", "pending", "leased", "running", "expired", "foreign", "older", "overflow", "accepted"} {
		t.Run(state, func(t *testing.T) {
			journal, path, binding, outcome := managedJournalFixture(t)
			proposal, err := journal.propose("original", binding, outcome, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "cancelled"}
			switch state {
			case "foreign":
				observed.Result.TmuxSessionName = "changed"
			case "older":
				observed.CurrentLeaseAttempt = 2
			case "overflow":
				observed.CurrentLeaseAttempt = 2147483648
			case "accepted":
				observed.Accepted = true
			default:
				observed.TaskStatus = state
			}
			err = journal.retire(*proposal, observed)
			if state != "cancelled" {
				if !errors.Is(err, ledger.ErrConflict) {
					t.Fatal("invalid evidence retired proposal", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			reopened, err := ledger.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			journal.ledger = reopened
			saved, err := journal.load("original")
			if err != nil || saved.record != proposal.record {
				t.Fatal("retirement changed original proposal", err)
			}
			if state != "cancelled" {
				if saved.entry.Status != managedStartPending {
					t.Fatal("invalid evidence changed journal")
				}
				return
			}
			if saved.entry.Status != managedStartRetired || saved.retirement != observed {
				t.Fatal("lost exact cancellation evidence")
			}
			if err := journal.confirm(*proposal, outcome); !errors.Is(err, ledger.ErrConflict) {
				t.Fatal("stale confirmation revived retired proposal", err)
			}
			client := &startConfirmationFixture{}
			if committed, err := confirmManagedStart(context.Background(), client, journal, "original", binding, outcome, proposal, nil); committed || err == nil || client.sent != 0 {
				t.Fatal("stale publication resent retired outcome", err)
			}
			if _, err := journal.propose("original", binding, outcome, saved, nil); !errors.Is(err, ledger.ErrConflict) {
				t.Fatal("retirement permitted new proposal", err)
			}
			if err := inspectPendingManagedStarts(context.Background(), client, journal, binding.NodeID); err != nil || client.reads != 0 {
				t.Fatal("retired record remained in inspection loop", err)
			}
			w := Worker{ledger: journal.ledger}
			if err := w.executeTask(context.Background(), api.TaskEnvelope{TaskID: "original", TaskType: "create_tool_session"}); !errors.Is(err, errManagedSessionPending) {
				t.Fatal("retired record entered legacy execution", err)
			}
		})
	}
}

func TestManagedStartBackgroundCancellationAndExpiry(t *testing.T) {
	for _, state := range []string{"cancelled", "expired", "leased", "running", "pending"} {
		t.Run(state, func(t *testing.T) {
			journal, _, binding, outcome := managedJournalFixture(t)
			if _, err := journal.propose("original", binding, outcome, nil, nil); err != nil {
				t.Fatal(err)
			}
			client := &startConfirmationFixture{observe: api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: state}}
			for range 2 {
				if err := inspectPendingManagedStarts(context.Background(), client, journal, binding.NodeID); err != nil {
					t.Fatal(err)
				}
			}
			expectedReads := 2
			if state == "cancelled" {
				expectedReads = 1
			}
			if client.sent != 0 || client.reads != expectedReads {
				t.Fatal("background inspection published or retained cancelled proposal")
			}
		})
	}
}

func TestManagedStartRetirementCannotReplaceNewerOrConfirmedProposal(t *testing.T) {
	for _, replacement := range []string{"confirmed", "newer"} {
		t.Run(replacement, func(t *testing.T) {
			journal, _, binding, outcome := managedJournalFixture(t)
			proposal, err := journal.propose("original", binding, outcome, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if replacement == "confirmed" {
				if err := journal.confirm(*proposal, outcome); err != nil {
					t.Fatal(err)
				}
			} else {
				newer, err := api.NewManagedSessionStartResult(binding, 4, "stopped", "")
				if err != nil {
					t.Fatal(err)
				}
				absence := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 4, TaskStatus: "leased"}
				if _, err := journal.propose("original", binding, newer, proposal, &absence); err != nil {
					t.Fatal(err)
				}
			}
			before, err := journal.load("original")
			if err != nil {
				t.Fatal(err)
			}
			observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 4, TaskStatus: "cancelled"}
			if err := journal.retire(*proposal, observed); !errors.Is(err, ledger.ErrConflict) {
				t.Fatal("stale cancellation replaced current proposal", err)
			}
			after, err := journal.load("original")
			if err != nil || after.record != before.record || after.entry.Status != before.entry.Status {
				t.Fatal("stale cancellation changed record", err)
			}
		})
	}
}

func TestManagedStartConcurrentRetirementSurvivesReopen(t *testing.T) {
	journal, path, binding, outcome := managedJournalFixture(t)
	proposal, err := journal.propose("original", binding, outcome, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 4, TaskStatus: "cancelled"}
	var writers sync.WaitGroup
	for range 8 {
		writers.Go(func() {
			if err := journal.retire(*proposal, observed); err != nil {
				t.Error(err)
			}
		})
	}
	writers.Wait()
	reopened, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	journal.ledger = reopened
	saved, err := journal.load("original")
	if err != nil || saved.entry.Status != managedStartRetired || saved.retirement != observed || saved.record != proposal.record {
		t.Fatal("lost concurrent retirement", err)
	}
	if err := journal.retire(*saved, observed); err != nil {
		t.Fatal("identical retirement did not replay", err)
	}
}

func TestManagedStartRetirementWriteFailureRemainsPending(t *testing.T) {
	journal, path, binding, outcome := managedJournalFixture(t)
	proposal, err := journal.propose("original", binding, outcome, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".pending"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "cancelled"}
	if err := journal.retire(*proposal, observed); err == nil {
		t.Fatal("failed retirement reported success")
	}
	saved, err := journal.load("original")
	if err != nil || saved.entry.Status != managedStartPending || saved.record != proposal.record {
		t.Fatal("failed write discarded proposal", err)
	}
}

func TestManagedStartRetirementRejectsCorruptEvidence(t *testing.T) {
	for _, corruption := range []string{"missing", "null", "alias", "extra", "accepted", "attempt", "outcome", "status", "pending", "confirmed"} {
		t.Run(corruption, func(t *testing.T) {
			journal, path, binding, outcome := managedJournalFixture(t)
			proposal, err := journal.propose("original", binding, outcome, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "cancelled"}
			if err := journal.retire(*proposal, observed); err != nil {
				t.Fatal(err)
			}
			saved, err := journal.load("original")
			if err != nil {
				t.Fatal(err)
			}
			evidence := saved.entry.Result["retirement"].(map[string]any)
			switch corruption {
			case "missing":
				delete(saved.entry.Result, "retirement")
			case "null":
				saved.entry.Result["retirement"] = nil
			case "alias":
				evidence["Accepted"] = evidence["accepted"]
				delete(evidence, "accepted")
			case "extra":
				evidence["path"] = "/untrusted"
			case "accepted":
				evidence["accepted"] = true
			case "attempt":
				evidence["current_lease_attempt"] = 2
			case "outcome":
				evidence["result"].(map[string]any)["tmux_session_name"] = "other"
			case "status":
				evidence["task_status"] = "expired"
			case "pending":
				saved.entry.Status = managedStartPending
			case "confirmed":
				saved.entry.Status = managedStartConfirmed
			}
			if err := journal.ledger.Save(saved.entry); err != nil {
				t.Fatal(err)
			}
			reopened, err := ledger.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved, err := (managedStartJournal{ledger: reopened}).load("original"); err == nil || saved != nil {
				t.Fatal("corrupt retirement became usable", err)
			}
		})
	}
}

type cancelledSnapshotFixture struct {
	*snapshotPreparationFixture
	*startConfirmationFixture
}

func TestManagedStartupCancelledProposalNeverTouchesRuntime(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "observed", true: "retained"}[retired], func(t *testing.T) {
			journal, _, binding, outcome := managedJournalFixture(t)
			proposal, err := journal.propose("original-task", binding, outcome, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			observed := api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "cancelled"}
			if retired {
				if err := journal.retire(*proposal, observed); err != nil {
					t.Fatal(err)
				}
			}
			f := &cancelledSnapshotFixture{snapshotPreparationFixture: newSnapshotPreparationFixture(t), startConfirmationFixture: &startConfirmationFixture{observe: observed}}
			// Nil Helper and peer make any accidental recovery, preparation or launch fail the test.
			result, err := startAndConfirmManagedSnapshot(context.Background(), f, nil, journal, "original-task", binding, 3, f.session, nil)
			if !errors.Is(err, errManagedStartRetired) || result != (api.ManagedSessionStartResult{}) || f.sent != 0 || len(f.calls) != 0 {
				t.Fatal("cancelled proposal obtained runtime authority", err)
			}
			expectedReads := 1
			if retired {
				expectedReads = 0
			}
			if f.reads != expectedReads {
				t.Fatal("retired startup repeated inspection")
			}
		})
	}
}

func TestManagedStartupFailedRetirementStillReportsPending(t *testing.T) {
	journal, path, binding, outcome := managedJournalFixture(t)
	if _, err := journal.propose("original-task", binding, outcome, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".pending"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	f := &cancelledSnapshotFixture{
		snapshotPreparationFixture: newSnapshotPreparationFixture(t),
		startConfirmationFixture:   &startConfirmationFixture{observe: api.ManagedStartObservation{Result: outcome, CurrentLeaseAttempt: 3, TaskStatus: "cancelled"}},
	}
	result, err := startAndConfirmManagedSnapshot(context.Background(), f, nil, journal, "original-task", binding, 3, f.session, nil)
	if !errors.Is(err, errManagedSessionPending) || errors.Is(err, errManagedStartRetired) || result != (api.ManagedSessionStartResult{}) || f.sent != 0 || len(f.calls) != 0 {
		t.Fatal("failed local retirement became completed or touched runtime", err)
	}
	saved, err := journal.load("original-task")
	if err != nil || saved.entry.Status != managedStartPending {
		t.Fatal("failed retirement discarded recovery", err)
	}
}
