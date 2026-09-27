package egobrowser

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestVerifyToolSessionPeerNeverCreatesOrRestoresAuthorization(t *testing.T) {
	broker := testBrokerWithoutBindings(t)
	var grants atomic.Int32
	broker.cfg.GrantPeerAccess = func(string, uint32) error { grants.Add(1); return nil }
	nonce := registerTestSession(t, broker, "original-session")
	for range 2 {
		if err := broker.VerifyToolSessionPeer("original-session", nonce, 12345); !errors.Is(err, ErrUnavailable) {
			t.Fatal("registration without original grant recovered", err)
		}
	}
	if grants.Load() != 0 || broker.listener != nil {
		t.Fatal("verification performed socket or ACL mutations")
	}
	if err := broker.AuthorizeToolSessionPeer("original-session", nonce, 12345); err != nil {
		t.Fatal(err)
	}
	if err := broker.VerifyToolSessionPeer("original-session", nonce, 12345); err != nil || grants.Load() != 1 {
		t.Fatal("original admitted peer did not recover without regrant", err)
	}
	for _, attempt := range []struct {
		session string
		nonce   string
		uid     uint32
	}{
		{"different-session", nonce, 12345},
		{"original-session", "different-nonce", 12345},
		{"original-session", nonce, 12346},
		{"original-session", nonce, 0},
	} {
		if err := broker.VerifyToolSessionPeer(attempt.session, attempt.nonce, attempt.uid); err == nil {
			t.Fatal("foreign peer recovered")
		}
	}
	broker.UnregisterToolSession("original-session", nonce)
	rotated := registerTestSession(t, broker, "original-session")
	for _, candidate := range []string{nonce, rotated} {
		if err := broker.VerifyToolSessionPeer("original-session", candidate, 12345); !errors.Is(err, ErrUnavailable) {
			t.Fatal("revoked peer recovered through re-registration", err)
		}
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := broker.VerifyToolSessionPeer("original-session", rotated, 12345); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed broker recovered peer", err)
	}
	if grants.Load() != 1 {
		t.Fatal("verification restored an expired grant")
	}
}
