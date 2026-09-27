package skillexport

import (
	"context"
	"sync"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Authorization is connection-local and starts before any potentially long Helper scan.
// Network serialization never holds mu, so expiry can cancel a blocked renewal or write.
type exportAuthorization struct {
	mu         sync.Mutex
	refreshMu  sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	workers    sync.WaitGroup
	changed    chan struct{}
	identity   Identity
	grant      string
	initial    skillmanager.NodeExportPermission
	current    skillmanager.NodeExportPermission
	authority  Authority
	observed   *Header
	validUntil time.Time
	refreshAt  time.Time
}

func newExportAuthorization(ctx context.Context, cancel context.CancelFunc, identity Identity, grant string, initial skillmanager.NodeExportPermission, authority Authority, started time.Time) *exportAuthorization {
	a := &exportAuthorization{ctx: ctx, cancel: cancel, changed: make(chan struct{}, 1), identity: identity,
		grant: grant, initial: initial, current: initial, authority: authority}
	a.setWindow(started, initial)
	a.workers.Add(2)
	go a.watchExpiry()
	go a.renew()
	return a
}

func (a *exportAuthorization) close() { a.cancel(); a.workers.Wait() }

// setWindow measures validity from request start, never response arrival.
// Its caller holds mu once the background workers have started.
func (a *exportAuthorization) setWindow(started time.Time, p skillmanager.NodeExportPermission) {
	interval := time.Duration(p.RecheckSeconds) * time.Second
	a.validUntil = started.Add(interval)
	if p.ExpiresAt.Before(a.validUntil) {
		a.validUntil = p.ExpiresAt
	}
	a.refreshAt = started.Add(a.validUntil.Sub(started) / 2)
}

func (a *exportAuthorization) validLocked() bool {
	if a.ctx.Err() != nil || !time.Now().Before(a.validUntil) {
		a.cancel()
		return false
	}
	return true
}

func (a *exportAuthorization) watchExpiry() {
	defer a.workers.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		a.mu.Lock()
		if !a.validLocked() {
			a.mu.Unlock()
			return
		}
		remaining := time.Until(a.validUntil)
		a.mu.Unlock()
		timer.Reset(remaining)
		select {
		case <-a.ctx.Done():
			return
		case <-a.changed:
		case <-timer.C:
		}
	}
}

func (a *exportAuthorization) renew() {
	defer a.workers.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if a.check() != nil {
				return
			}
		}
	}
}

func (a *exportAuthorization) observe(header Header, force bool) error {
	a.mu.Lock()
	var err error
	if a.observed == nil {
		err = a.initial.MatchExport(header.Binding, header.TreeDigest, header.Unclean)
		if err == nil {
			err = a.current.MatchExport(header.Binding, header.TreeDigest, header.Unclean)
		}
		if err == nil {
			// Renewal validates every new permission against these immutable facts.
			a.observed = &Header{Binding: header.Binding, TreeDigest: header.TreeDigest, Unclean: header.Unclean}
		}
	} else if a.observed.Binding != header.Binding || a.observed.TreeDigest != header.TreeDigest || a.observed.Unclean != header.Unclean {
		err = ErrUnavailable
	}
	if err != nil {
		a.cancel()
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return a.verify(force)
}

func (a *exportAuthorization) check() error   { return a.verify(false) }
func (a *exportAuthorization) refresh() error { return a.verify(true) }

func (a *exportAuthorization) needsRefresh(force bool) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.validLocked() {
		return false, ErrUnavailable
	}
	return force || !time.Now().Before(a.refreshAt), nil
}

func (a *exportAuthorization) verify(force bool) error {
	needed, err := a.needsRefresh(force)
	if err != nil || !needed {
		return err
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	// Coalesce object checks that waited for the same periodic renewal.
	needed, err = a.needsRefresh(force)
	if err != nil || !needed {
		return err
	}
	started := time.Now()
	i := a.identity
	p, nextGrant, renewed, err := a.nextPermission()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.validLocked() || err != nil || p.Validate() != nil || !matchesIdentity(p, i) || p.Binding != a.initial.Binding ||
		!renewed && !p.ExpiresAt.Equal(a.initial.ExpiresAt) || !p.ExpiresAt.After(time.Now()) {
		a.cancel()
		return ErrUnavailable
	}
	if a.observed != nil && p.MatchExport(a.observed.Binding, a.observed.TreeDigest, a.observed.Unclean) != nil {
		a.cancel()
		return ErrUnavailable
	}
	// Facts already reported by Server cannot disappear or change on a later check.
	if a.current.IncomingDigest != nil && (p.IncomingDigest == nil || *p.IncomingDigest != *a.current.IncomingDigest) ||
		a.current.Unclean != nil && (p.Unclean == nil || *p.Unclean != *a.current.Unclean) {
		a.cancel()
		return ErrUnavailable
	}
	a.current = p
	a.grant = nextGrant
	a.setWindow(started, p)
	if !a.validLocked() {
		return ErrUnavailable
	}
	select {
	case a.changed <- struct{}{}:
	default:
	}
	return nil
}

// refreshMu owns the credential; a response can replace it only after the old window check.
func (a *exportAuthorization) nextPermission() (skillmanager.NodeExportPermission, string, bool, error) {
	i := a.identity
	if authority, ok := a.authority.(RenewalAuthority); ok {
		result, err := authority.RenewNodeExport(a.ctx, i.NodeID, i.SnapshotID, i.DeviceID, i.SSHKeyID, a.grant)
		if err == nil {
			err = result.MatchPrevious(a.grant)
		}
		return result.Permission, result.Grant, true, err
	}
	p, err := a.authority.VerifyNodeExport(a.ctx, i.NodeID, i.SnapshotID, i.DeviceID, i.SSHKeyID, a.grant)
	return p, a.grant, false, err
}
