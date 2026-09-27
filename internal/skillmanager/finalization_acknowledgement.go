package skillmanager

import (
	"errors"
	"reflect"
)

// FinalizationAcknowledgement binds a Node-authenticated Server receipt to the full frozen capture.
// The authorized worker supplies it after durable HTTP receipt validation. It contains no credentials
// and is not a cryptographic Server signature or permission to delete the retained objects.
type FinalizationAcknowledgement struct {
	Version     int                 `json:"version"`
	Capture     FinalizationRecord  `json:"capture"`
	Receipt     FinalizationReceipt `json:"receipt"`
	Publication *PublicationReceipt `json:"publication"`
}

// Input derives the immutable transfer identity rather than accepting a replacement key.
func (ack FinalizationAcknowledgement) Input() FinalizationInput {
	return FinalizationInput{SnapshotID: ack.Capture.Binding.SnapshotID, SessionID: ack.Capture.Binding.SessionID,
		IdempotencyKey: "skill-finalization:" + ack.Capture.Binding.SnapshotID, TreeDigest: ack.Capture.TreeDigest, Unclean: ack.Capture.Unclean}
}

// Validate requires the exact complete capture and consistent independent persistence/publication.
func (ack FinalizationAcknowledgement) Validate() error {
	if ack.Version != 1 || ack.Capture.ObjectsVersion != 1 || ack.Capture.Validate() != nil {
		return errors.New("invalid finalization acknowledgement identity")
	}
	if err := ack.Receipt.ValidateReceipt(ack.Input(), nil); err != nil {
		return err
	}
	if ack.Publication != nil {
		return ack.Publication.ValidateReceipt(ack.Input(), ack.Receipt)
	}
	return nil
}

// State returns the local phase proved by this receipt; superseded never becomes a terminal phase.
func (ack FinalizationAcknowledgement) State() (string, error) {
	if err := ack.Validate(); err != nil {
		return "", err
	}
	if ack.Publication != nil && ack.Publication.Status != "superseded" {
		return ack.Publication.Status, nil
	}
	if ack.Receipt.CheckpointID == nil {
		return "upload_pending", nil
	}
	if ack.Capture.Unclean {
		return "persisted_unclean", nil
	}
	return "persisted", nil
}

// Follows rejects changed original inputs, older uploads and replacement publication decisions.
func (ack FinalizationAcknowledgement) Follows(previous FinalizationAcknowledgement) error {
	if err := ack.Validate(); err != nil {
		return err
	}
	if err := previous.Validate(); err != nil {
		return err
	}
	if !SameFinalizationInput(ack.Capture, previous.Capture) {
		return errors.New("acknowledgement changed original finalization")
	}
	if err := ack.Receipt.ValidateReceipt(ack.Input(), &previous.Receipt); err != nil {
		return err
	}
	if previous.Publication != nil && !reflect.DeepEqual(previous.Publication, ack.Publication) {
		return errors.New("acknowledgement changed publication decision")
	}
	return nil
}

// SameFinalizationInput compares the complete frozen identity while allowing local state progress.
func SameFinalizationInput(first, second FinalizationRecord) bool {
	first.State, second.State = "", ""
	return first == second
}

// UnmarshalJSON rejects omitted/null identity fields and requires an explicit nullable publication.
func (ack *FinalizationAcknowledgement) UnmarshalJSON(data []byte) error {
	type plain FinalizationAcknowledgement
	var decoded plain
	if err := decodeFinalizationFields(data, &decoded, []string{"version", "capture", "receipt"}, []string{"publication"}); err != nil {
		return err
	}
	*ack = FinalizationAcknowledgement(decoded)
	return nil
}
