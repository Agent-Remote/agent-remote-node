package skillexport

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type authorizationFunc func(context.Context) (skillmanager.NodeExportPermission, error)

func (f authorizationFunc) VerifyNodeExport(ctx context.Context, _, _, _, _, _ string) (skillmanager.NodeExportPermission, error) {
	return f(ctx)
}

func ownedAuthorization(t *testing.T, f *exportFixture, authority Authority, started time.Time) *exportAuthorization {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a := newExportAuthorization(ctx, cancel, f.identity(), exportGrant, f.permission, authority, started)
	t.Cleanup(a.close)
	return a
}

func TestExportObjectChecksShareOnlyConnectionLocalValidity(t *testing.T) {
	f := frozenFixture(t)
	f.permission.RecheckSeconds = 10
	a := ownedAuthorization(t, f, f, time.Now())
	header := Header{Binding: f.permission.Binding, TreeDigest: f.record.TreeDigest, Unclean: f.record.Unclean}
	if err := a.observe(header, true); err != nil {
		t.Fatal(err)
	}
	for range 200000 {
		if err := a.observe(header, false); err != nil {
			t.Fatal(err)
		}
	}
	if calls := f.checks.Load(); calls != 1 {
		t.Fatal("per-object validity performed HTTP calls", calls)
	}
	if err := a.observe(header, true); err != nil || f.checks.Load() != 2 {
		t.Fatal("completion did not force remote verification", err)
	}
	other := ownedAuthorization(t, f, f, time.Now())
	if err := other.observe(header, true); err != nil || f.checks.Load() != 3 {
		t.Fatal("connection reused another connection's header proof", err)
	}
}

func TestExportRenewalExpiryClosesBlockedOutput(t *testing.T) {
	f := frozenFixture(t)
	var calls atomic.Int32
	authority := authorizationFunc(func(ctx context.Context) (skillmanager.NodeExportPermission, error) {
		if calls.Add(1) >= 3 {
			<-ctx.Done()
			return skillmanager.NodeExportPermission{}, ctx.Err()
		}
		return f.permission, nil
	})
	reader, writer := io.Pipe()
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	started := time.Now()
	err := Serve(ctx, &blockedConnection{connection().in, writer}, f.identity(), authority, f)
	if !errors.Is(err, ErrUnavailable) || ctx.Err() != nil || time.Since(started) > 2*time.Second || calls.Load() != 3 {
		t.Fatal("blocked HTTP renewal extended stale authority", err, calls.Load(), time.Since(started))
	}
}

func TestExportInitialNetworkDelayConsumesValidity(t *testing.T) {
	f := frozenFixture(t)
	a := ownedAuthorization(t, f, f, time.Now().Add(-2*time.Second))
	if a.check() == nil || a.ctx.Err() == nil || f.checks.Load() != 0 {
		t.Fatal("expired initial response revived authorization")
	}
}

func TestExportShorterIntervalUsesRenewalRequestStart(t *testing.T) {
	f := frozenFixture(t)
	f.permission.RecheckSeconds = 10
	short := f.permission
	short.RecheckSeconds = 1
	authority := authorizationFunc(func(ctx context.Context) (skillmanager.NodeExportPermission, error) {
		timer := time.NewTimer(1100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return short, nil
		case <-ctx.Done():
			return skillmanager.NodeExportPermission{}, ctx.Err()
		}
	})
	a := ownedAuthorization(t, f, authority, time.Now())
	if a.refresh() == nil || a.ctx.Err() == nil || a.check() == nil {
		t.Fatal("late shortened validity was accepted or revived")
	}
}

func TestExportShorterIntervalAppliesDuringBlockedRenewal(t *testing.T) {
	f := frozenFixture(t)
	f.permission.RecheckSeconds = 10
	short := f.permission
	short.RecheckSeconds = 1
	var calls atomic.Int32
	authority := authorizationFunc(func(ctx context.Context) (skillmanager.NodeExportPermission, error) {
		if calls.Add(1) == 1 {
			return short, nil
		}
		<-ctx.Done()
		return skillmanager.NodeExportPermission{}, ctx.Err()
	})
	a := ownedAuthorization(t, f, authority, time.Now())
	started := time.Now()
	if err := a.refresh(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old ten-second interval survived shortened server permission")
	}
	if calls.Load() != 2 || time.Since(started) < 900*time.Millisecond {
		t.Fatal("renewal did not run within shortened validity", calls.Load())
	}
}

func TestExportRenewalCannotChangeIdentityOrForgetFacts(t *testing.T) {
	for _, fault := range []string{"binding", "device", "key", "expiry", "digest", "classification", "invalid_interval", "denied"} {
		t.Run(fault, func(t *testing.T) {
			f := frozenFixture(t)
			f.permission.RecheckSeconds = 10
			unclean := false
			f.permission.IncomingDigest, f.permission.Unclean = &f.record.TreeDigest, &unclean
			next := f.permission
			switch fault {
			case "binding":
				next.Binding.DirectoryEpoch++
			case "device":
				next.DeviceID = f.permission.SSHKeyID
			case "key":
				next.SSHKeyID = f.permission.DeviceID
			case "expiry":
				next.ExpiresAt = next.ExpiresAt.Add(time.Second)
			case "digest":
				next.IncomingDigest = nil
			case "classification":
				next.IncomingDigest, next.Unclean = nil, nil
			case "invalid_interval":
				next.RecheckSeconds = 11
			}
			var calls atomic.Int32
			authority := authorizationFunc(func(context.Context) (skillmanager.NodeExportPermission, error) {
				calls.Add(1)
				if fault == "denied" {
					return next, ErrUnavailable
				}
				return next, nil
			})
			a := ownedAuthorization(t, f, authority, time.Now())
			if a.refresh() == nil || a.ctx.Err() == nil || a.refresh() == nil || a.check() == nil || calls.Load() != 1 {
				t.Fatal("invalid authority was not terminal")
			}
		})
	}
}

func TestExportCallerCancellationJoinsBlockedRenewal(t *testing.T) {
	f := frozenFixture(t)
	entered := make(chan struct{})
	authority := authorizationFunc(func(ctx context.Context) (skillmanager.NodeExportPermission, error) {
		close(entered)
		<-ctx.Done()
		return skillmanager.NodeExportPermission{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newExportAuthorization(ctx, cancel, f.identity(), exportGrant, f.permission, authority, time.Now())
	defer a.close()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("renewal did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() { a.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation leaked renewal workers")
	}
	if a.check() == nil {
		t.Fatal("cancelled connection retained authority")
	}
}

func TestExportLateSuccessfulRenewalCannotReviveExpiredWindow(t *testing.T) {
	f := frozenFixture(t)
	authority := authorizationFunc(func(context.Context) (skillmanager.NodeExportPermission, error) {
		// Simulate a response racing cancellation rather than a context-aware transport error.
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		return f.permission, nil
	})
	a := ownedAuthorization(t, f, authority, time.Now().Add(-800*time.Millisecond))
	if a.refresh() == nil || a.ctx.Err() == nil || a.check() == nil {
		t.Fatal("successful late HTTP response revived an expired connection")
	}
}

func TestExportWindowCannotOutliveGrantExpiry(t *testing.T) {
	f := frozenFixture(t)
	f.permission.RecheckSeconds = 10
	f.permission.ExpiresAt = time.Now().Add(200 * time.Millisecond)
	a := ownedAuthorization(t, f, f, time.Now())
	select {
	case <-a.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("recheck interval extended original grant expiry")
	}
	if a.refresh() == nil || a.check() == nil {
		t.Fatal("expired grant retained authority")
	}
}
