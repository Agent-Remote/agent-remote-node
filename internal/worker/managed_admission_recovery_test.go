package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type admissionRecoveryFixture struct {
	*finalizationFixture
	drains     int
	drainError error
	disabled   bool
}

func (f *admissionRecoveryFixture) DrainUnadmittedSkillSession(_ context.Context, _, nodeID, sessionID string) (runtimehelper.SkillSessionObservation, error) {
	f.drains++
	if nodeID != f.capture.Binding.NodeID || sessionID != f.capture.Binding.SessionID {
		f.t.Fatal("drain changed original identity")
	}
	if f.disabled {
		return *f.observation, nil
	}
	return runtimehelper.SkillSessionObservation{SessionID: sessionID, State: "finalized", Record: &f.capture}, f.drainError
}

func TestFinalizationAdmissionRequiresOriginalProcessGrant(t *testing.T) {
	for _, kind := range []string{"original", "restart", "revoked", "replacement", "new_grant", "changed_uid", "disabled", "uncertain", "not_started"} {
		t.Run(kind, func(t *testing.T) {
			w, session, grants := managedPeerFixture(t)
			w.managedAdmissions = newManagedRuntimeAdmissions()
			peer, err := w.prepareManagedSessionPeer(session)
			if err != nil {
				t.Fatal(err)
			}
			if err := peer.authorize(context.Background(), 12345); err != nil {
				t.Fatal(err)
			}
			w.managedAdmissions.remember(peer)
			f := &admissionRecoveryFixture{finalizationFixture: newFinalizationFixture(t, true, "detached")}
			f.observation = &runtimehelper.SkillSessionObservation{SessionID: session.SessionID, State: "running"}
			f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: session.SessionID, Code: "not_finalized"}}}
			switch kind {
			case "not_started":
				f.observation.State = "not_started"
			case "restart", "disabled":
				w.managedAdmissions = newManagedRuntimeAdmissions()
				f.disabled = kind == "disabled"
			case "changed_uid":
				grant := w.managedAdmissions.granted[session.SessionID]
				grant.uid++
				w.managedAdmissions.granted[session.SessionID] = grant
			case "revoked", "replacement", "new_grant", "uncertain":
				w.browserBroker.UnregisterToolSession(session.SessionID, peer.registration.nonce)
				if kind == "replacement" || kind == "new_grant" {
					replacement, err := w.prepareManagedSessionPeer(session)
					if err != nil {
						t.Fatal(err)
					}
					if kind == "new_grant" {
						if err := w.browserBroker.AuthorizeToolSessionPeer(session.SessionID, replacement.registration.nonce, 12345); err != nil {
							t.Fatal(err)
						}
						if err := replacement.recover(context.Background(), 12345); err == nil {
							t.Fatal("replacement grant adopted original managed runtime")
						}
					}
				}
				if kind == "uncertain" {
					f.drainError = errors.New("writers remain")
				}
			}
			before := grants.Load()
			reader := managedFinalizationReader{managedAdmissionHelper: f, admissions: w.managedAdmissions, broker: w.browserBroker}
			_, err = recoverFinalizationPage(context.Background(), f.client, reader, f.journal, f.capture.Binding.NodeID, "")
			if (err != nil) != (kind == "uncertain") {
				t.Fatal("unexpected admission recovery result", err)
			}
			if grants.Load() != before || (f.drains == 0) != (kind == "original" || kind == "not_started") {
				t.Fatal("recovery granted access or drained the original admitted peer", f.drains)
			}
			if kind == "original" || kind == "disabled" || kind == "uncertain" || kind == "not_started" {
				if len(f.recordedCalls()) != 0 || f.cleanupCalls != 0 {
					t.Fatal("running or uncertain runtime uploaded or cleaned")
				}
				if kind == "not_started" && (len(w.managedAdmissions.granted) != 0 || w.browserBroker.VerifyToolSessionPeer(session.SessionID, peer.registration.nonce, 12345) == nil) {
					t.Fatal("unstarted session retained obsolete admission")
				}
			} else if f.cleanupCalls != 1 || len(w.managedAdmissions.granted) != 0 {
				t.Fatal("drained runtime did not transfer and retire admission")
			}
		})
	}
}

func TestFinalizationAdmissionWaitsForStartupOwnership(t *testing.T) {
	w, session, grants := managedPeerFixture(t)
	w.managedAdmissions = newManagedRuntimeAdmissions()
	f := &admissionRecoveryFixture{finalizationFixture: newFinalizationFixture(t, false, "published")}
	f.observation = &runtimehelper.SkillSessionObservation{SessionID: session.SessionID, State: "running"}
	reader := managedFinalizationReader{managedAdmissionHelper: f, admissions: w.managedAdmissions, broker: w.browserBroker}
	if err := w.managedAdmissions.lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := reader.ReconcileSkillSession(ctx, "blocked", f.capture.Binding.NodeID, session.SessionID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("blocked observation ignored cancellation", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := reader.ReconcileSkillSession(context.Background(), "concurrent", f.capture.Binding.NodeID, session.SessionID)
		result <- err
	}()
	peer, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.authorize(context.Background(), 12345); err != nil {
		t.Fatal(err)
	}
	w.managedAdmissions.remember(peer)
	w.managedAdmissions.unlock()
	select {
	case err := <-result:
		if err != nil || f.drains != 0 || grants.Load() != 1 {
			t.Fatal("background inspection raced ahead of startup admission", err)
		}
	case <-time.After(time.Second):
		t.Fatal("background inspection did not release startup wait")
	}
}

func TestFinalizationInventoryRetiresOnlyOriginalGrant(t *testing.T) {
	for _, code := range []string{"", "reclamation_pending", "content_reclaimed"} {
		t.Run(code, func(t *testing.T) {
			w, session, grants := managedPeerFixture(t)
			w.managedAdmissions = newManagedRuntimeAdmissions()
			peer, err := w.prepareManagedSessionPeer(session)
			if err != nil {
				t.Fatal(err)
			}
			if err := peer.authorize(context.Background(), 12345); err != nil {
				t.Fatal(err)
			}
			w.managedAdmissions.remember(peer)
			w.browserBroker.UnregisterToolSession(session.SessionID, peer.registration.nonce)
			nonce, _, err := w.browserBroker.RegisterToolSession(session.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.browserBroker.AuthorizeToolSessionPeer(session.SessionID, nonce, 12345); err != nil {
				t.Fatal(err)
			}
			f := &admissionRecoveryFixture{finalizationFixture: newFinalizationFixture(t, false, "published")}
			if code != "" {
				f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: session.SessionID, Code: code}}}
			}
			reader := managedFinalizationReader{managedAdmissionHelper: f, admissions: w.managedAdmissions, broker: w.browserBroker}
			if _, err := reader.ListSkillFinalizations(context.Background(), "already-frozen", f.capture.Binding.NodeID, ""); err != nil {
				t.Fatal(err)
			}
			if len(w.managedAdmissions.granted) != 0 || f.drains != 0 || grants.Load() != 2 {
				t.Fatal("frozen inventory retained admission or invoked runtime work")
			}
			if err := w.browserBroker.VerifyToolSessionPeer(session.SessionID, nonce, 12345); err != nil {
				t.Fatal("stale managed admission retired a later registration", err)
			}
		})
	}
}
