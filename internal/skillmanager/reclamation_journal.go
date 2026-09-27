package skillmanager

import "errors"

// ErrFinalizationReclaiming excludes a durably marked input from transfer and recapture.
var ErrFinalizationReclaiming = errors.New("skill finalization content reclamation is pending")

// ErrFinalizationReclaimed reports deliberately removed content without erasing its audit identity.
var ErrFinalizationReclaimed = errors.New("skill finalization content was reclaimed")

// ReclamationDirectory binds a deletion intent to one existing directory, never a replacement path.
type ReclamationDirectory struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

// FinalizationReclamation is immutable local deletion intent, not a reusable remote grant.
// Its creation requires privileged caller proofs of writer, mount and reference absence.
type FinalizationReclamation struct {
	Version       int                      `json:"version"`
	Capture       FinalizationRecord       `json:"capture"`
	Authorization ReclamationAuthorization `json:"authorization"`
	SessionRoot   string                   `json:"session_root"`
	Work          ReclamationDirectory     `json:"work"`
	Objects       ReclamationDirectory     `json:"objects"`
}

// UnmarshalJSON requires the complete immutable intent with no ambiguous or omitted fields.
func (r *FinalizationReclamation) UnmarshalJSON(data []byte) error {
	type plain FinalizationReclamation
	return decodeJournalFields(data, (*plain)(r), []string{"version", "capture", "authorization", "session_root", "work", "objects"}, nil)
}

// UnmarshalJSON preserves exact 64-bit filesystem identities and rejects incomplete bindings.
func (r *ReclamationDirectory) UnmarshalJSON(data []byte) error {
	type plain ReclamationDirectory
	return decodeJournalFields(data, (*plain)(r), []string{"device", "inode"}, nil)
}
