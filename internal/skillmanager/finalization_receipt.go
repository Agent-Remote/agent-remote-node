package skillmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// FinalizationInput identifies one immutable, Helper-retained stopped-session capture.
// It is not writer-exit evidence; the caller must obtain that evidence from the Helper.
type FinalizationInput struct {
	SnapshotID     string
	SessionID      string
	IdempotencyKey string
	TreeDigest     string
	Unclean        bool
}

// FinalizationReceipt distinguishes input persistence from account publication.
type FinalizationReceipt struct {
	ID             string    `json:"id"`
	SnapshotID     string    `json:"snapshot_id"`
	IncomingDigest string    `json:"incoming_digest"`
	Unclean        bool      `json:"unclean"`
	Status         string    `json:"status"`
	CheckpointID   *string   `json:"checkpoint_id"`
	UploadID       string    `json:"upload_id"`
	UploadAttempt  int64     `json:"upload_attempt"`
	UploadStatus   string    `json:"upload_status"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// PublicationReceipt records the whole-directory publication decision, including retained conflicts.
type PublicationReceipt struct {
	ID                 string  `json:"id"`
	FinalizationID     string  `json:"finalization_id"`
	Attempt            int64   `json:"attempt"`
	Status             string  `json:"status"`
	Reason             *string `json:"reason"`
	ResultCheckpointID *string `json:"result_checkpoint_id"`
	ConflictCount      int64   `json:"conflict_count"`
}

var finalizationKey = regexp.MustCompile(`^[!-~]{1,128}$`)
var publicationReason = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)

// ValidateReceipt checks a retained receipt and, when supplied, its original upload history.
// It performs no HTTP request and does not prove that caller-supplied data came from the Server.
func (view FinalizationReceipt) ValidateReceipt(input FinalizationInput, previous *FinalizationReceipt) error {
	if err := view.validate(input); err != nil {
		return err
	}
	if previous != nil {
		if err := previous.validate(input); err != nil {
			return err
		}
		return view.follows(*previous)
	}
	return nil
}

// ValidateReceipt checks a publication against the separately retained incoming checkpoint.
func (view PublicationReceipt) ValidateReceipt(input FinalizationInput, retained FinalizationReceipt) error {
	if err := retained.validate(input); err != nil {
		return err
	}
	if retained.CheckpointID == nil || !validSkillUUID(view.ID) || view.FinalizationID != retained.ID || view.Attempt <= 0 || view.ConflictCount < 0 ||
		(view.Reason != nil && !publicationReason.MatchString(*view.Reason)) {
		return errors.New("publication receipt differs from retained finalization")
	}
	valid := false
	switch view.Status {
	case "published":
		valid = !input.Unclean && view.Reason == nil && view.ConflictCount == 0 && view.ResultCheckpointID != nil && validSkillUUID(*view.ResultCheckpointID)
	case "conflicted":
		valid = !input.Unclean && view.Reason == nil && view.ConflictCount > 0 && view.ResultCheckpointID == nil
	case "detached":
		valid = view.Reason != nil && view.ConflictCount == 0 && view.ResultCheckpointID == nil && (!input.Unclean || *view.Reason == "unclean")
	case "superseded":
		valid = !input.Unclean && view.Reason != nil && view.ResultCheckpointID == nil
	}
	if !valid {
		return errors.New("inconsistent finalization publication decision")
	}
	return nil
}

// Validate checks the original finalization identity before transfer or acknowledgement.
func (input FinalizationInput) Validate() error {
	if !validSkillUUID(input.SnapshotID) || !validSkillUUID(input.SessionID) ||
		!finalizationKey.MatchString(input.IdempotencyKey) || !contentDigestPattern.MatchString(input.TreeDigest) {
		return errors.New("invalid retained finalization identity")
	}
	return nil
}

func (view FinalizationReceipt) validate(input FinalizationInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if !validSkillUUID(view.ID) || view.SnapshotID != input.SnapshotID || view.IncomingDigest != input.TreeDigest || view.Unclean != input.Unclean ||
		!validSkillUUID(view.UploadID) || view.UploadAttempt <= 0 || view.ExpiresAt.IsZero() {
		return errors.New("finalization receipt changed the retained input")
	}
	if view.Status == "upload_pending" {
		if view.CheckpointID == nil && (view.UploadStatus == "staged" || view.UploadStatus == "expired") {
			return nil
		}
		return errors.New("incomplete finalization claimed retained content")
	}
	if view.UploadStatus != "committed" || view.CheckpointID == nil || !validSkillUUID(*view.CheckpointID) {
		return errors.New("finalization lacks a retained input checkpoint")
	}
	switch view.Status {
	case "persisted", "published", "conflicted":
		if !view.Unclean {
			return nil
		}
	case "persisted_unclean":
		if view.Unclean {
			return nil
		}
	case "detached":
		return nil
	}
	return errors.New("invalid finalization phase or termination classification")
}

func (view FinalizationReceipt) follows(previous FinalizationReceipt) error {
	if view.ID != previous.ID || view.UploadAttempt < previous.UploadAttempt ||
		(view.UploadAttempt == previous.UploadAttempt) != (view.UploadID == previous.UploadID) {
		return errors.New("finalization receipt changed its original upload authority")
	}
	if previous.CheckpointID != nil && (view.CheckpointID == nil || *view.CheckpointID != *previous.CheckpointID || view.UploadID != previous.UploadID) {
		return errors.New("finalization receipt lost its retained input checkpoint")
	}
	return nil
}

// UnmarshalJSON requires every canonical receipt field, including explicit nullable checkpoints.
func (view *FinalizationReceipt) UnmarshalJSON(data []byte) error {
	type plain FinalizationReceipt
	var decoded plain
	if err := decodeFinalizationFields(data, &decoded,
		[]string{"id", "snapshot_id", "incoming_digest", "unclean", "status", "upload_id", "upload_attempt", "upload_status", "expires_at"},
		[]string{"checkpoint_id"}); err != nil {
		return err
	}
	*view = FinalizationReceipt(decoded)
	return nil
}

// UnmarshalJSON rejects missing, extra, aliased, duplicate and incorrectly null publication fields.
func (view *PublicationReceipt) UnmarshalJSON(data []byte) error {
	type plain PublicationReceipt
	var decoded plain
	if err := decodeFinalizationFields(data, &decoded,
		[]string{"id", "finalization_id", "attempt", "status", "conflict_count"},
		[]string{"reason", "result_checkpoint_id"}); err != nil {
		return err
	}
	*view = PublicationReceipt(decoded)
	return nil
}

func decodeFinalizationFields(data []byte, out any, required, nullable []string) error {
	if err := rejectReceiptDuplicates(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != len(required)+len(nullable) {
		return errors.New("invalid finalization receipt fields")
	}
	for _, name := range required {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing finalization receipt field")
		}
	}
	for _, name := range nullable {
		if fields[name] == nil {
			return errors.New("missing nullable finalization receipt field")
		}
	}
	return json.Unmarshal(data, out)
}

// All receipt objects have flat scalar fields. Nested acknowledgement values invoke their own
// strict decoders; reject duplicate top-level keys before a map can discard them.
func rejectReceiptDuplicates(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("receipt must be a JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return errors.New("duplicate receipt field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	return nil
}
