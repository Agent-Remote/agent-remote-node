package skillmanager

import (
	"errors"
	"time"
)

// ReclamationAuthorization is a fresh Server observation of the complete original input.
// It is not a content lease, a cryptographic signature or local writer/reference proof.
type ReclamationAuthorization struct {
	Version            int       `json:"version"`
	RequestID          string    `json:"request_id"`
	NodeID             string    `json:"node_id"`
	UserID             string    `json:"user_id"`
	AccountID          string    `json:"account_id"`
	SessionID          string    `json:"session_id"`
	SnapshotID         string    `json:"snapshot_id"`
	FinalizationID     string    `json:"finalization_id"`
	CheckpointID       string    `json:"checkpoint_id"`
	TreeDigest         string    `json:"tree_digest"`
	Unclean            bool      `json:"unclean"`
	PublicationID      string    `json:"publication_id"`
	PublicationAttempt int64     `json:"publication_attempt"`
	PublicationStatus  string    `json:"publication_status"`
	VerifiedAt         time.Time `json:"verified_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// Match requires the original frozen input and a separately verified terminal acknowledgement.
// A later terminal publication may retain the same incoming checkpoint; it cannot replace its bytes.
func (a ReclamationAuthorization) Match(ack FinalizationAcknowledgement) error {
	state, err := ack.State()
	if err != nil || ack.Publication == nil || (state != "published" && state != "conflicted" && state != "detached") {
		return errors.New("reclamation requires original terminal acknowledgement")
	}
	for _, value := range []string{a.RequestID, a.NodeID, a.UserID, a.AccountID, a.SessionID, a.SnapshotID, a.FinalizationID, a.CheckpointID, a.PublicationID} {
		if !validSkillUUID(value) {
			return errors.New("invalid reclamation authorization identity")
		}
	}
	if a.Version != 1 || a.VerifiedAt.IsZero() || a.ExpiresAt.Sub(a.VerifiedAt) != time.Minute ||
		a.PublicationAttempt <= 0 || a.PublicationAttempt < ack.Publication.Attempt ||
		(a.PublicationAttempt == ack.Publication.Attempt) != (a.PublicationID == ack.Publication.ID) ||
		a.PublicationAttempt == ack.Publication.Attempt && a.PublicationStatus != ack.Publication.Status {
		return errors.New("invalid reclamation authorization lifetime or publication")
	}
	if a.PublicationStatus != "published" && a.PublicationStatus != "conflicted" && a.PublicationStatus != "detached" ||
		a.Unclean && a.PublicationStatus != "detached" {
		return errors.New("reclamation authorization requires terminal publication")
	}
	binding := ack.Capture.Binding
	if a.NodeID != binding.NodeID || a.UserID != binding.UserID || a.AccountID != binding.AccountID ||
		a.SessionID != binding.SessionID || a.SnapshotID != binding.SnapshotID ||
		a.FinalizationID != ack.Receipt.ID || ack.Receipt.CheckpointID == nil || a.CheckpointID != *ack.Receipt.CheckpointID ||
		a.TreeDigest != ack.Capture.TreeDigest || a.Unclean != ack.Capture.Unclean {
		return errors.New("reclamation authorization changed original input")
	}
	return nil
}

// UnmarshalJSON rejects missing, null, duplicate, aliased and extra authorization fields.
func (a *ReclamationAuthorization) UnmarshalJSON(data []byte) error {
	type plain ReclamationAuthorization
	var decoded plain
	if err := decodeFinalizationFields(data, &decoded, []string{
		"version", "request_id", "node_id", "user_id", "account_id", "session_id", "snapshot_id", "finalization_id", "checkpoint_id",
		"tree_digest", "unclean", "publication_id", "publication_attempt", "publication_status", "verified_at", "expires_at",
	}, nil); err != nil {
		return err
	}
	*a = ReclamationAuthorization(decoded)
	return nil
}
