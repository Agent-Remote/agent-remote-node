package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

type snapshotConfirmationClient interface {
	snapshotPreparationClient
	managedStartConfirmationClient
}

// startAndConfirmManagedSnapshot returns historical Server acceptance, not a reusable liveness grant.
// Unconfirmed tasks recover the original runtime under one lease before proposing any new outcome.
func startAndConfirmManagedSnapshot(ctx context.Context, client snapshotConfirmationClient, helper snapshotStartupHelper, journal managedStartJournal, requestID string, binding skillmanager.SkillSnapshotIdentity, attempt int64, session toolsessions.CreatePayload, peer snapshotPeerAdmission) (api.ManagedSessionStartResult, error) {
	if err := validateSnapshotPreparationInput(requestID, binding, attempt, session); err != nil {
		return api.ManagedSessionStartResult{}, err
	}
	previous, err := journal.load(requestID)
	if err != nil {
		return api.ManagedSessionStartResult{}, err
	}
	var observation *api.ManagedStartObservation
	if previous != nil {
		if previous.record.Binding != binding {
			return api.ManagedSessionStartResult{}, errors.New("pending startup binding changed")
		}
		if previous.entry.Status == managedStartRetired {
			return api.ManagedSessionStartResult{}, errManagedStartRetired
		}
		original := previous.record.Outcome
		observed, err := client.InspectManagedSessionStart(ctx, requestID, binding, original.LeaseAttempt, original)
		if err != nil || observed.Result != original {
			return api.ManagedSessionStartResult{}, errors.Join(errManagedSessionPending, err)
		}
		if observed.Accepted {
			return original, journal.confirm(*previous, original)
		}
		if observed.TaskStatus == "cancelled" {
			if err := journal.retire(*previous, observed); err != nil {
				return api.ManagedSessionStartResult{}, errors.Join(errManagedSessionPending, err)
			}
			return api.ManagedSessionStartResult{}, errManagedStartRetired
		}
		if previous.entry.Status == managedStartConfirmed || observed.CurrentLeaseAttempt != attempt || attempt < original.LeaseAttempt {
			return api.ManagedSessionStartResult{}, errManagedSessionPending
		}
		observation = &observed
	}
	var accepted api.ManagedSessionStartResult
	publish := func(workCtx context.Context, status string) (bool, error) {
		tmux := ""
		if status == "running" {
			tmux = session.TmuxSessionName
		}
		outcome, err := api.NewManagedSessionStartResult(binding, attempt, status, tmux)
		if err != nil {
			return false, err
		}
		committed, err := confirmManagedStart(workCtx, client, journal, requestID, binding, outcome, previous, observation)
		if committed {
			accepted = outcome
		}
		return committed, err
	}
	_, err = startManagedSnapshotWithPublication(ctx, client, helper, requestID, binding, attempt, session, peer, publish)
	return accepted, err
}
