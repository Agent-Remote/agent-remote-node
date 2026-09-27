package skillmanager

import (
	"encoding/json"
	"errors"
)

// CaptureFailure confirms original stopped writers without inventing a frozen content identity.
// It grants no file read, upload, cleanup or reclamation authority.
type CaptureFailure struct {
	Binding SnapshotBinding `json:"binding"`
	Unclean bool            `json:"unclean"`
	Code    string          `json:"code"`
}

// Validate requires the original managed task and one content-free diagnostic.
func (r CaptureFailure) Validate() error {
	if r.Binding.Validate() != nil || !validSkillUUID(r.Binding.TaskID) {
		return errors.New("invalid pending capture identity")
	}
	switch r.Code {
	case "quota_exceeded", "insufficient_storage", "portability_error", "capture_failed":
		return nil
	default:
		return errors.New("invalid pending capture code")
	}
}

// UnmarshalJSON rejects missing, duplicate, aliased and null observation fields.
func (r *CaptureFailure) UnmarshalJSON(data []byte) error {
	type plain CaptureFailure
	var value plain
	if err := decodeFinalizationFields(data, &value, []string{"binding", "unclean", "code"}, nil); err != nil {
		return err
	}
	*r = CaptureFailure(value)
	return r.Validate()
}

var _ json.Unmarshaler = (*CaptureFailure)(nil)
