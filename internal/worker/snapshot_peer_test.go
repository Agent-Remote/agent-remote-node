package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowser"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

func TestManagedStartupRecoveryDrainsRuntimeAfterPeerRevocation(t *testing.T) {
	w, session, grants := managedPeerFixture(t)
	peer, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	f := &snapshotStartupFixture{snapshotPreparationFixture: newSnapshotPreparationFixture(t)}
	f.session = peer.session
	if _, err := startManagedSnapshot(context.Background(), f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session, peer); err != nil {
		t.Fatal(err)
	}
	f.recovered = true
	f.calls = nil
	if _, err := startManagedSnapshot(context.Background(), f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session, peer); err != nil {
		t.Fatal("original broker peer could not recover", err)
	}
	if !reflect.DeepEqual(f.calls, []string{"snapshot", "recover"}) || grants.Load() != 1 {
		t.Fatal("recovery repeated preparation or peer grants", f.calls)
	}
	w.browserBroker.UnregisterToolSession(session.SessionID, peer.session.EgoBrowserBrokerNonce)
	replacement, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	f.session, f.calls = replacement.session, nil
	result, err := startManagedSnapshot(context.Background(), f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session, replacement)
	if result != nil || !errors.Is(err, errManagedSessionPending) || !errors.Is(err, egobrowser.ErrUnavailable) {
		t.Fatal("revoked original peer was treated as recovered", err)
	}
	if !reflect.DeepEqual(f.calls, []string{"snapshot", "recover", "cancel"}) || grants.Load() != 1 {
		t.Fatal("failed peer recovery did not exclusively drain the original runtime", f.calls)
	}
}

func managedPeerFixture(t *testing.T) (Worker, toolsessions.CreatePayload, *atomic.Int32) {
	t.Helper()
	root, err := os.MkdirTemp("", "ar-managed-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	grants := &atomic.Int32{}
	broker, err := egobrowser.New(egobrowser.Config{
		Enabled: true, NodeID: "node-test", StateRoot: root, SocketPath: filepath.Join(root, "broker.sock"),
		GrantPeerAccess: func(string, uint32) error { grants.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	admission := &atomic.Bool{}
	admission.Store(true)
	w := Worker{cfg: config.Config{EgoBrowserEnabled: true, EgoBrowserBrokerRoot: root, EgoBrowserBrokerSocket: filepath.Join(root, "broker.sock")}.WithDefaults(), browserBroker: broker, serverExecutionAdmission: admission}
	session := newSnapshotPreparationFixture(t).session
	session.TmuxSessionName, session.SandboxName = "managed-original", "unused-native"
	return w, session, grants
}

func TestManagedSessionPeerBrokerRestartCannotAdoptOriginalRuntime(t *testing.T) {
	w, session, grants := managedPeerFixture(t)
	original, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.authorize(context.Background(), 12345); err != nil {
		t.Fatal(err)
	}
	if err := w.browserBroker.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := egobrowser.New(egobrowser.Config{
		Enabled: true, NodeID: "node-test", StateRoot: w.cfg.EgoBrowserBrokerRoot, SocketPath: w.cfg.EgoBrowserBrokerSocket,
		GrantPeerAccess: func(string, uint32) error { grants.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	w.browserBroker = restarted
	for range 2 {
		peer, err := w.prepareManagedSessionPeer(session)
		if err != nil {
			t.Fatal(err)
		}
		if peer.session.EgoBrowserBrokerNonce == original.session.EgoBrowserBrokerNonce {
			t.Fatal("broker restart restored a process-local nonce")
		}
		if err := peer.recover(context.Background(), 12345); !errors.Is(err, egobrowser.ErrUnavailable) {
			t.Fatal("restarted broker adopted live runtime", err)
		}
	}
	if grants.Load() != 1 {
		t.Fatal("broker restart granted replacement access during recovery")
	}
}

func TestManagedSessionPeerRecoveryRequiresOriginalGrant(t *testing.T) {
	w, session, grants := managedPeerFixture(t)
	session.EgoBrowserBrokerNonce = "task-controlled-nonce"
	first, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	if !first.session.EgoBrowserEnabled || first.session.EgoBrowserBrokerNonce == "" || first.session.EgoBrowserBrokerNonce == session.EgoBrowserBrokerNonce {
		t.Fatal("untrusted broker nonce survived preparation")
	}
	ctx := context.Background()
	for range 2 {
		peer, err := w.prepareManagedSessionPeer(session)
		if err != nil {
			t.Fatal(err)
		}
		if err := peer.recover(ctx, 12345); !errors.Is(err, egobrowser.ErrUnavailable) {
			t.Fatal("repeated registration substituted for original authorization", err)
		}
	}
	if grants.Load() != 0 {
		t.Fatal("recovery granted replacement peer access")
	}
	if err := first.authorize(ctx, 12345); err != nil {
		t.Fatal(err)
	}
	retry, err := w.prepareManagedSessionPeer(session)
	if err != nil || retry.session.EgoBrowserBrokerNonce != first.session.EgoBrowserBrokerNonce {
		t.Fatal("same-process retry lost original nonce", err)
	}
	if err := retry.recover(ctx, 12345); err != nil || grants.Load() != 1 {
		t.Fatal("original peer not recovered without mutation", err)
	}
	if err := retry.recover(ctx, 12346); err == nil {
		t.Fatal("changed runtime UID recovered")
	}
	w.browserBroker.UnregisterToolSession(first.session.SessionID, first.session.EgoBrowserBrokerNonce)
	replacement, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	first.rollbackRegistration()
	if _, created, err := w.browserBroker.RegisterToolSession(session.SessionID); err != nil || created {
		t.Fatal("stale rollback revoked replacement registration", err)
	}
	if err := replacement.recover(ctx, 12345); !errors.Is(err, egobrowser.ErrUnavailable) || grants.Load() != 1 {
		t.Fatal("new nonce adopted an existing runtime", err)
	}
}

func TestManagedSessionPeerCancellationAndDisabledBrowser(t *testing.T) {
	w, session, grants := managedPeerFixture(t)
	peer, err := w.prepareManagedSessionPeer(session)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, check := range []func(context.Context, uint32) error{peer.authorize, peer.recover} {
		if err := check(ctx, 12345); !errors.Is(err, context.Canceled) {
			t.Fatal("peer admission ignored cancellation", err)
		}
		if err := check(context.Background(), 0); err == nil {
			t.Fatal("root peer accepted")
		}
	}
	if grants.Load() != 0 {
		t.Fatal("cancelled admission changed socket grants")
	}
	w.cfg.EgoBrowserEnabled = false
	session.EgoBrowserEnabled, session.EgoBrowserBrokerNonce = true, "task-controlled-nonce"
	disabled, err := w.prepareManagedSessionPeer(session)
	if err != nil || disabled.session.EgoBrowserEnabled || disabled.session.EgoBrowserBrokerNonce != "" {
		t.Fatal("disabled browser retained task-controlled context", err)
	}
	if err := disabled.recover(context.Background(), 12345); err != nil || grants.Load() != 0 {
		t.Fatal("browser-disabled runtime required a broker grant", err)
	}
}

func TestManagedSessionPeerInvalidInputOnlyRollsBackNewRegistration(t *testing.T) {
	w, session, _ := managedPeerFixture(t)
	invalid := session
	invalid.TmuxSessionName = ""
	if _, err := w.prepareManagedSessionPeer(invalid); err == nil {
		t.Fatal("invalid launch input accepted")
	}
	nonce, created, err := w.browserBroker.RegisterToolSession(session.SessionID)
	if err != nil || !created {
		t.Fatal("invalid input retained new registration", err)
	}
	if err := w.browserBroker.AuthorizeToolSessionPeer(session.SessionID, nonce, 12345); err != nil {
		t.Fatal(err)
	}
	if _, err := w.prepareManagedSessionPeer(invalid); err == nil {
		t.Fatal("invalid retry accepted")
	}
	if err := w.browserBroker.VerifyToolSessionPeer(session.SessionID, nonce, 12345); err != nil {
		t.Fatal("invalid retry revoked preexisting original peer", err)
	}
}
