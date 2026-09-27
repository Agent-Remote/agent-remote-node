package worker

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

type snapshotConfirmationFixture struct {
	*snapshotStartupFixture
	journal managedStartJournal
	path    string
	mode    string
}

func (f *snapshotConfirmationFixture) RecoverManagedSession(ctx context.Context, task string, input runtimehelper.ManagedSessionSpecRequest) (map[string]any, error) {
	if f.mode == "stopped" {
		f.calls = append(f.calls, "recover")
		f.checkRequest(task, input)
		return nil, &runtimehelper.Error{Code: "SKILL_START_STOPPED"}
	}
	return f.snapshotStartupFixture.RecoverManagedSession(ctx, task, input)
}

func (f *snapshotConfirmationFixture) ConfirmManagedSessionStart(ctx context.Context, task string, binding api.SkillSnapshotIdentity, attempt int64, outcome api.ManagedSessionStartResult) (api.ManagedSessionStartResult, error) {
	f.calls = append(f.calls, "confirm")
	if ctx.Err() != nil || task != "original-task" || binding != f.snapshot.SkillSnapshotIdentity || attempt != 3 {
		f.t.Error("confirmation escaped original live lease")
	}
	pending, err := f.journal.load(task)
	if err != nil || pending == nil || pending.record.Outcome != outcome || pending.entry.Status != managedStartPending {
		f.t.Error("confirmation preceded its original durable proposal", err)
	}
	switch f.mode {
	case "lost_response":
		return api.ManagedSessionStartResult{}, errors.New("uncertain confirmation")
	case "lease_race":
		f.stopRenewal.Store(true)
		<-ctx.Done()
	case "local_write":
		if err := os.Rename(f.path, f.path+".pending"); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Mkdir(f.path, 0o700); err != nil {
			f.t.Fatal(err)
		}
	}
	return outcome, nil
}

func (f *snapshotConfirmationFixture) InspectManagedSessionStart(context.Context, string, api.SkillSnapshotIdentity, int64, api.ManagedSessionStartResult) (api.ManagedStartObservation, error) {
	f.t.Error("new startup unexpectedly inspected a prior proposal")
	return api.ManagedStartObservation{}, errors.New("no prior proposal")
}

func TestManagedStartupKeepsLeaseThroughDurableServerConfirmation(t *testing.T) {
	for _, mode := range []string{"ready", "stopped", "lease_race", "lost_response", "local_write"} {
		t.Run(mode, func(t *testing.T) {
			journal, path, _, _ := managedJournalFixture(t)
			startup := &snapshotStartupFixture{snapshotPreparationFixture: newSnapshotPreparationFixture(t)}
			startup.session.TmuxSessionName = "original"
			f := &snapshotConfirmationFixture{snapshotStartupFixture: startup, journal: journal, path: path, mode: mode}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			receipt, err := startAndConfirmManagedSnapshot(ctx, f, f, journal, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session, f)
			pending, loadErr := journal.load("original-task")
			if loadErr != nil || pending == nil {
				t.Fatal("startup lost its durable outcome", loadErr)
			}
			if mode == "lost_response" {
				if !errors.Is(err, errManagedSessionPending) || receipt != (api.ManagedSessionStartResult{}) || pending.entry.Status != managedStartPending || f.calls[len(f.calls)-1] != "cancel" {
					t.Fatal("unknown commit failed to retain and drain original startup", err, f.calls)
				}
				return
			}
			if mode == "local_write" {
				if err == nil || pending.entry.Status != managedStartPending {
					t.Fatal("local failure lost original pending acknowledgement", err)
				}
			} else if err != nil || pending.entry.Status != managedStartConfirmed {
				t.Fatal("original commit was lost", err)
			}
			if receipt != pending.record.Outcome || f.calls[len(f.calls)-1] != "confirm" {
				t.Fatal("verified receipt triggered runtime cancellation", f.calls)
			}
			if mode == "stopped" {
				if receipt.Code != "SKILL_START_STOPPED" || !reflect.DeepEqual(f.calls, []string{"snapshot", "recover", "confirm"}) {
					t.Fatal("stopped recovery launched or prepared again", f.calls)
				}
			} else if receipt.Status != "running" {
				t.Fatal("ready receipt changed outcome")
			}
		})
	}
}
