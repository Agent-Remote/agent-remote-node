package worker

import (
	"context"
	"errors"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

var errManagedSessionPending = errors.New("SKILL_START_PENDING: managed session requires retained-state recovery")

type snapshotStartupHelper interface {
	snapshotPreparationHelper
	RecoverManagedSession(context.Context, string, runtimehelper.ManagedSessionSpecRequest) (map[string]any, error)
	StartManagedSession(context.Context, string, runtimehelper.ManagedSessionSpecRequest) (map[string]any, error)
	CancelManagedSession(context.Context, string, runtimehelper.ManagedSessionSpecRequest) error
}

type snapshotPeerAdmission interface {
	authorize(context.Context, uint32) error
	recover(context.Context, uint32) error
}

type snapshotStartPublication func(context.Context, string) (bool, error)

// startManagedSnapshot owns one lease through recovery, preparation, peer admission and launch.
// The caller must retain the original broker registration; this cannot rotate a live runtime nonce.
// Errors are nonterminal, including an uncertain cleanup. No path authorizes replacement execution.
func startManagedSnapshot(ctx context.Context, client snapshotPreparationClient, helper snapshotStartupHelper, requestID string, binding skillmanager.SkillSnapshotIdentity, attempt int64, session toolsessions.CreatePayload, peer snapshotPeerAdmission) (map[string]any, error) {
	return startManagedSnapshotWithPublication(ctx, client, helper, requestID, binding, attempt, session, peer, nil)
}

func startManagedSnapshotWithPublication(ctx context.Context, client snapshotPreparationClient, helper snapshotStartupHelper, requestID string, binding skillmanager.SkillSnapshotIdentity, attempt int64, session toolsessions.CreatePayload, peer snapshotPeerAdmission, publish snapshotStartPublication) (map[string]any, error) {
	if err := validateSnapshotPreparationInput(requestID, binding, attempt, session); err != nil {
		return nil, err
	}
	if peer == nil {
		return nil, errors.New("managed startup requires explicit runtime peer admission")
	}
	var request runtimehelper.ManagedSessionSpecRequest
	var result map[string]any
	var committed bool
	var publicationErr error
	publishOutcome := func(ctx context.Context, status string) error {
		if publish == nil {
			return nil
		}
		committed, publicationErr = publish(ctx, status)
		return publicationErr
	}
	touchedHelper := false
	err := withSnapshotLease(ctx, client, binding, attempt, func(workCtx context.Context) error {
		snapshot, err := client.GetSkillSnapshot(workCtx, binding)
		if err != nil {
			return err
		}
		if err := snapshot.Validate(binding); err != nil {
			return err
		}
		request, err = runtimehelper.NewManagedSessionSpecRequest(snapshot, session)
		if err != nil {
			return err
		}
		if err := workCtx.Err(); err != nil {
			return err
		}
		touchedHelper = true
		result, err = helper.RecoverManagedSession(workCtx, requestID, request)
		if err != nil {
			var stopped *runtimehelper.Error
			if publish != nil && errors.As(err, &stopped) && stopped.Code == "SKILL_START_STOPPED" {
				result = map[string]any{"status": "stopped"}
				return publishOutcome(workCtx, "stopped")
			}
			return err
		}
		if err := workCtx.Err(); err != nil {
			return err
		}
		if result["status"] == "running" {
			uid, err := takeRuntimeUID(result)
			if err != nil {
				return err
			}
			if err := peer.recover(workCtx, uid); err != nil {
				return err
			}
			return publishOutcome(workCtx, "running")
		}
		if result["status"] != "not_started" {
			return errManagedSessionPending
		}
		prepared, err := prepareManagedSnapshotInput(workCtx, client, helper, requestID, snapshot, request)
		if err != nil {
			return err
		}
		if err := workCtx.Err(); err != nil {
			return err
		}
		if err := peer.authorize(workCtx, prepared.RuntimeUID); err != nil {
			return err
		}
		if err := workCtx.Err(); err != nil {
			return err
		}
		result, err = helper.StartManagedSession(workCtx, requestID, request)
		if err != nil {
			return err
		}
		uid, err := takeRuntimeUID(result)
		if err != nil || uid != prepared.RuntimeUID {
			return errManagedSessionPending
		}
		return publishOutcome(workCtx, "running")
	})
	// Committing closes Server lease authority. Its exact receipt remains valid even if renewal
	// observes that terminal state first, or local acknowledgement publication subsequently fails.
	if committed {
		return result, publicationErr
	}
	if publish != nil && err == nil {
		err = errManagedSessionPending
	}
	if err == nil {
		return result, nil
	}
	if touchedHelper {
		// A successful socket response can race lease loss. Independently drain the original
		// runtime even when launch already returned; socket cancellation alone cannot cover it.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cleanupErr := helper.CancelManagedSession(cleanupCtx, requestID, request)
		cancel()
		err = errors.Join(err, cleanupErr)
	}
	return nil, errors.Join(errManagedSessionPending, err)
}
