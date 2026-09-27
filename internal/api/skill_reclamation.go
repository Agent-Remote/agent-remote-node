package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillReclamationGrant retains a fresh response and its conservative process-local deadline.
// Deadline has a monotonic component and must never be persisted or reconstructed after restart.
type SkillReclamationGrant struct {
	Authorization skillmanager.ReclamationAuthorization
	Deadline      time.Time
}

// AuthorizeSkillReclamation rechecks Server content before a separate local reclamation decision.
// It never retries, deletes content or infers authority from host/Server wall-clock agreement.
func (c Client) AuthorizeSkillReclamation(ctx context.Context, ack skillmanager.FinalizationAcknowledgement) (SkillReclamationGrant, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return SkillReclamationGrant{}, err
	}
	nonce[6], nonce[8] = nonce[6]&0x0f|0x40, nonce[8]&0x3f|0x80
	challenge := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:])
	return c.authorizeSkillReclamation(ctx, ack, challenge)
}

// AuthorizeSkillReclamationChallenge echoes a live Helper challenge through authenticated HTTP.
// The Helper must start its own monotonic budget before issuing this challenge. No deadline crosses
// this boundary; the response alone cannot authorize marking or regain time spent in IPC.
func (c Client) AuthorizeSkillReclamationChallenge(ctx context.Context, challenge string, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error) {
	grant, err := c.authorizeSkillReclamation(ctx, ack, challenge)
	return grant.Authorization, err
}

func (c Client) authorizeSkillReclamation(ctx context.Context, ack skillmanager.FinalizationAcknowledgement, challenge string) (SkillReclamationGrant, error) {
	state, err := ack.State()
	if err != nil || ack.Publication == nil || (state != "published" && state != "conflicted" && state != "detached") {
		return SkillReclamationGrant{}, errors.New("reclamation requires saved terminal acknowledgement")
	}
	if !validSkillUUID(challenge) {
		return SkillReclamationGrant{}, errors.New("reclamation requires a canonical live challenge")
	}
	path := finalizationPath(ack.Receipt, "/reclamation-authorization", false) + "?" + url.Values{"request_id": {challenge}}.Encode()
	started := time.Now()
	var response struct {
		skillEnvelope[skillmanager.ReclamationAuthorization]
		Retryable *bool `json:"retryable"`
	}
	if err := c.skillRequestLimit(ctx, http.MethodGet, path, "application/json", nil, &response, 16*1024); err != nil {
		return SkillReclamationGrant{}, err
	}
	if response.SchemaVersion != 1 || response.Status != "reclaimable" || response.Committed == nil || !*response.Committed ||
		response.Retryable == nil || *response.Retryable || len(response.Errors) != 0 || response.Data.RequestID != challenge {
		return SkillReclamationGrant{}, errors.New("invalid reclamation response envelope or request binding")
	}
	if err := response.Data.Match(ack); err != nil {
		return SkillReclamationGrant{}, err
	}
	// Charge the entire request, including Server scanning, against the short authorization window.
	deadline := started.Add(response.Data.ExpiresAt.Sub(response.Data.VerifiedAt))
	if !time.Now().Before(deadline) || ctx.Err() != nil {
		return SkillReclamationGrant{}, errors.New("reclamation authorization budget expired")
	}
	return SkillReclamationGrant{Authorization: response.Data, Deadline: deadline}, nil
}
