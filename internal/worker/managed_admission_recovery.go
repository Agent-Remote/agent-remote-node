package worker

import (
	"context"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowser"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type managedRuntimeAdmission struct {
	nonce string
	uid   uint32
}

// The gate serializes startup and admission inspection, including Helper calls. A background
// observation taken before admission must never drain a runtime launched just after that observation.
// Grants remain process-only: historical readiness and a new registration cannot restore them.
type managedRuntimeAdmissions struct {
	gate    chan struct{}
	granted map[string]managedRuntimeAdmission
}

func newManagedRuntimeAdmissions() *managedRuntimeAdmissions {
	return &managedRuntimeAdmissions{gate: make(chan struct{}, 1), granted: make(map[string]managedRuntimeAdmission)}
}

func (a *managedRuntimeAdmissions) lock(ctx context.Context) error {
	if a == nil {
		return errManagedSessionPending
	}
	select {
	case a.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			a.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *managedRuntimeAdmissions) unlock() { <-a.gate }

// remember is called under the gate before startup releases its ownership, even on uncertain exit.
func (a *managedRuntimeAdmissions) remember(peer *managedSessionPeer) {
	if peer.authorized && peer.registration != nil {
		a.granted[peer.session.SessionID] = managedRuntimeAdmission{nonce: peer.registration.nonce, uid: peer.runtimeUID}
	}
}

type managedAdmissionHelper interface {
	finalizationInventoryReader
	DrainUnadmittedSkillSession(context.Context, string, string, string) (runtimehelper.SkillSessionObservation, error)
}

type managedFinalizationReader struct {
	managedAdmissionHelper
	admissions *managedRuntimeAdmissions
	broker     *egobrowser.Broker
}

func (r managedFinalizationReader) ListSkillFinalizations(ctx context.Context, requestID, nodeID, cursor string) (skillmanager.FinalizationPage, error) {
	if err := r.admissions.lock(ctx); err != nil {
		return skillmanager.FinalizationPage{}, err
	}
	defer r.admissions.unlock()
	page, err := r.managedAdmissionHelper.ListSkillFinalizations(ctx, requestID, nodeID, cursor)
	if err != nil {
		return page, err
	}
	if err := page.Validate(cursor, nodeID); err != nil {
		return skillmanager.FinalizationPage{}, err
	}
	for _, item := range page.Items {
		if item.Record != nil || item.Code == "reclamation_pending" || item.Code == "content_reclaimed" {
			r.forget(item.SessionID)
		}
	}
	return page, nil
}

func (r managedFinalizationReader) forget(sessionID string) {
	if grant, exists := r.admissions.granted[sessionID]; exists && r.broker != nil {
		r.broker.UnregisterToolSession(sessionID, grant.nonce)
	}
	delete(r.admissions.granted, sessionID)
}

func (r managedFinalizationReader) ReconcileSkillSession(ctx context.Context, requestID, nodeID, sessionID string) (runtimehelper.SkillSessionObservation, error) {
	if err := r.admissions.lock(ctx); err != nil {
		return runtimehelper.SkillSessionObservation{}, err
	}
	defer r.admissions.unlock()
	observation, err := r.managedAdmissionHelper.ReconcileSkillSession(ctx, requestID, nodeID, sessionID)
	if err != nil {
		return observation, err
	}
	if err := observation.Validate(nodeID, sessionID); err != nil {
		return runtimehelper.SkillSessionObservation{}, err
	}
	if observation.State == "running" {
		grant, exists := r.admissions.granted[sessionID]
		if !exists || r.broker == nil || r.broker.VerifyToolSessionPeer(sessionID, grant.nonce, grant.uid) != nil {
			observation, err = r.DrainUnadmittedSkillSession(ctx, "finalization-admission-drain", nodeID, sessionID)
			if err != nil {
				return observation, err
			}
			if err := observation.Validate(nodeID, sessionID); err != nil {
				return runtimehelper.SkillSessionObservation{}, err
			}
		}
	}
	if observation.State == "finalized" || observation.State == "capture_pending" || observation.State == "not_started" {
		r.forget(sessionID)
	}
	return observation, nil
}
