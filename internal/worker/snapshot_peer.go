package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowser"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

type managedSessionPeer struct {
	session      toolsessions.CreatePayload
	broker       *egobrowser.Broker
	registration *egoBrowserSessionRegistration
	admitted     bool
	runtimeUID   uint32
	authorized   bool
	admissions   *managedRuntimeAdmissions
}

func (w Worker) prepareManagedSessionPeer(session toolsessions.CreatePayload) (*managedSessionPeer, error) {
	if session.ToolType != "claude" || session.RuntimeBackend != "native" {
		return nil, errors.New("managed session peer requires a Native Claude session")
	}
	payload, err := runtimehelper.Map(session)
	if err != nil {
		return nil, err
	}
	registration, err := w.applyEgoBrowserRuntimeContext(payload, "start_session")
	if err != nil {
		return nil, err
	}
	peer := &managedSessionPeer{broker: w.browserBroker, registration: registration, admissions: w.managedAdmissions}
	if registration != nil {
		if err := w.browserBroker.PrepareSocket(); err != nil {
			peer.rollbackRegistration()
			return nil, egoBrowserBrokerError("prepare managed session broker socket", err)
		}
	}
	peer.session, err = toolsessions.DecodeCreatePayload(payload)
	if err != nil {
		peer.rollbackRegistration()
		return nil, err
	}
	return peer, nil
}

func (p *managedSessionPeer) authorize(ctx context.Context, uid uint32) error {
	if err := p.validate(ctx, uid); err != nil {
		return err
	}
	if p.registration == nil {
		p.admitted = true
		return nil
	}
	if err := p.broker.AuthorizeToolSessionPeer(p.registration.toolSessionID, p.registration.nonce, uid); err != nil {
		return err
	}
	p.admitted = true
	p.runtimeUID = uid
	p.authorized = true
	return nil
}

func (p *managedSessionPeer) recover(ctx context.Context, uid uint32) error {
	if err := p.validate(ctx, uid); err != nil {
		return err
	}
	if p.registration == nil {
		p.admitted = true
		return nil
	}
	// Registration alone does not prove that this process admitted the original runtime.
	// This deliberately cannot grant an ACL or authorize a new nonce after broker restart.
	if p.admissions != nil {
		grant, exists := p.admissions.granted[p.session.SessionID]
		if !exists || grant.nonce != p.registration.nonce || grant.uid != uid {
			return errors.New("original managed runtime admission is unavailable")
		}
	}
	if err := p.broker.VerifyToolSessionPeer(p.registration.toolSessionID, p.registration.nonce, uid); err != nil {
		return err
	}
	p.admitted = true
	p.runtimeUID = uid
	return nil
}

func (p *managedSessionPeer) validate(ctx context.Context, uid uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || uid == 0 || p.session.EgoBrowserEnabled != (p.registration != nil) || p.registration != nil && p.broker == nil {
		return errors.New("managed session peer identity is unavailable")
	}
	if p.registration != nil && (p.registration.toolSessionID != p.session.SessionID || p.registration.nonce != p.session.EgoBrowserBrokerNonce) {
		return errors.New("managed session peer registration changed")
	}
	return nil
}

func (p *managedSessionPeer) rollbackRegistration() {
	if p.registration != nil && p.registration.created {
		p.broker.UnregisterToolSession(p.registration.toolSessionID, p.registration.nonce)
	}
}
