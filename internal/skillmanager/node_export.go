package skillmanager

import (
	"errors"
	"time"
)

// NodeExportBinding is the Server's immutable original snapshot identity for a frozen read.
type NodeExportBinding struct {
	SnapshotID        string `json:"snapshot_id"`
	SessionID         string `json:"session_id"`
	UserID            string `json:"user_id"`
	AccountID         string `json:"account_id"`
	NodeID            string `json:"node_id"`
	TaskID            string `json:"task_id"`
	LibraryGeneration int64  `json:"library_generation"`
	DirectoryEpoch    int64  `json:"directory_epoch"`
	InitialTreeDigest string `json:"initial_tree_digest"`
}

// ExportBinding selects the public original identity without the private preparation digest.
func (b SnapshotBinding) ExportBinding() NodeExportBinding {
	return NodeExportBinding{b.SnapshotID, b.SessionID, b.UserID, b.AccountID, b.NodeID, b.TaskID,
		b.LibraryGeneration, b.DirectoryEpoch, b.InitialTreeDigest}
}

// MatchExport checks a read observation without representing it as durable local finalization.
func (p NodeExportPermission) MatchExport(binding NodeExportBinding, digest string, unclean bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if binding != p.Binding || !contentDigestPattern.MatchString(digest) ||
		p.IncomingDigest != nil && *p.IncomingDigest != digest ||
		p.Unclean != nil && *p.Unclean != unclean {
		return errors.New("export observation differs from original authorization")
	}
	return nil
}

// NodeExportPermission authorizes one original read; it does not prove that a local capture exists.
type NodeExportPermission struct {
	Binding        NodeExportBinding `json:"binding"`
	DeviceID       string            `json:"device_id"`
	SSHKeyID       string            `json:"ssh_key_id"`
	ExpiresAt      time.Time         `json:"expires_at"`
	RecheckSeconds int               `json:"recheck_seconds"`
	IncomingDigest *string           `json:"incoming_digest"`
	Unclean        *bool             `json:"unclean"`
}

// Validate rejects incomplete original identities without reading any local state.
func (b NodeExportBinding) Validate() error {
	for _, id := range []string{b.SnapshotID, b.SessionID, b.UserID, b.AccountID, b.NodeID, b.TaskID} {
		if !validSkillUUID(id) {
			return errors.New("invalid frozen export identity")
		}
	}
	if b.LibraryGeneration < 0 || b.DirectoryEpoch < 1 || !contentDigestPattern.MatchString(b.InitialTreeDigest) {
		return errors.New("invalid frozen export generation or digest")
	}
	return nil
}

// Validate checks the complete current permission before the gateway can read frozen data.
func (p NodeExportPermission) Validate() error {
	if err := p.Binding.Validate(); err != nil {
		return err
	}
	if !validSkillUUID(p.DeviceID) || !validSkillUUID(p.SSHKeyID) || p.ExpiresAt.IsZero() ||
		p.RecheckSeconds < 1 || p.RecheckSeconds > 10 || p.IncomingDigest != nil && p.Unclean == nil ||
		p.IncomingDigest != nil && !contentDigestPattern.MatchString(*p.IncomingDigest) {
		return errors.New("invalid frozen export permission")
	}
	return nil
}

// MatchCapture requires the exact original binding and any separately reported termination fact.
func (p NodeExportPermission) MatchCapture(r FinalizationRecord) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.ObjectsVersion != 1 {
		return errors.New("frozen export capture differs from original authorization")
	}
	return p.MatchExport(r.Binding.ExportBinding(), r.TreeDigest, r.Unclean)
}

// UnmarshalJSON preserves canonical field names, integer precision and complete original identity.
func (b *NodeExportBinding) UnmarshalJSON(data []byte) error {
	type plain NodeExportBinding
	var value plain
	if err := decodeFinalizationFields(data, &value, []string{"snapshot_id", "session_id", "user_id", "account_id", "node_id", "task_id", "library_generation", "directory_epoch", "initial_tree_digest"}, nil); err != nil {
		return err
	}
	*b = NodeExportBinding(value)
	return b.Validate()
}

// UnmarshalJSON refuses missing or aliased nullable observations instead of guessing completeness.
func (p *NodeExportPermission) UnmarshalJSON(data []byte) error {
	type plain NodeExportPermission
	var value plain
	if err := decodeFinalizationFields(data, &value,
		[]string{"binding", "device_id", "ssh_key_id", "expires_at", "recheck_seconds"},
		[]string{"incoming_digest", "unclean"}); err != nil {
		return err
	}
	*p = NodeExportPermission(value)
	return p.Validate()
}
