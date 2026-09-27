package skillmanager

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
)

var snapshotInputUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

var snapshotSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// SkillSnapshotIdentity binds content access to the original task record and all runtime owners.
type SkillSnapshotIdentity struct {
	SnapshotID     string `json:"snapshot_id"`
	TaskID         string `json:"task_id"`
	NodeID         string `json:"node_id"`
	UserID         string `json:"user_id"`
	AccountID      string `json:"account_id"`
	SessionID      string `json:"session_id"`
	RuntimeBackend string `json:"runtime_backend"`
}

// Validate rejects unbound content requests before any HTTP or filesystem access.
func (b SkillSnapshotIdentity) Validate() error {
	for _, identity := range []string{b.SnapshotID, b.TaskID, b.NodeID, b.UserID, b.AccountID, b.SessionID} {
		if !validSkillUUID(identity) {
			return errors.New("invalid skill snapshot identity")
		}
	}
	if b.RuntimeBackend != "native" && b.RuntimeBackend != "docker_sandbox" {
		return errors.New("invalid skill snapshot backend")
	}
	return nil
}

// SkillSnapshot contains the Server's immutable preparation input, never a launch authorization.
type SkillSnapshot struct {
	SkillSnapshotIdentity
	LibraryGeneration    int64                      `json:"library_generation"`
	DirectoryEpoch       int64                      `json:"directory_epoch"`
	StartingCheckpointID *string                    `json:"starting_checkpoint_id"`
	TreeDigest           string                     `json:"tree_digest"`
	Manifest             Manifest                   `json:"manifest"`
	Items                []SkillSnapshotItem        `json:"items"`
	SystemReleases       map[string]json.RawMessage `json:"system_releases"`
}

// SkillSnapshotItem identifies one original materialized branch and its resolution.
type SkillSnapshotItem struct {
	StateID      string                  `json:"state_id"`
	EntryName    string                  `json:"entry_name"`
	StateEpoch   int64                   `json:"state_epoch"`
	CheckpointID string                  `json:"checkpoint_id"`
	Resolution   SkillSnapshotResolution `json:"resolution"`
}

// SkillSnapshotResolution preserves exact version selection without rereading current rules.
type SkillSnapshotResolution struct {
	Enabled         bool    `json:"enabled"`
	RevisionID      string  `json:"revision_id"`
	EnabledSource   string  `json:"enabled_source"`
	RevisionSource  string  `json:"revision_source"`
	Eligible        bool    `json:"eligible"`
	Included        bool    `json:"included"`
	ExclusionReason *string `json:"exclusion_reason"`
}

// Validate checks the complete immutable input against its authorized identity.
func (snapshot SkillSnapshot) Validate(expected SkillSnapshotIdentity) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if snapshot.SkillSnapshotIdentity != expected || snapshot.DirectoryEpoch < 1 || snapshot.LibraryGeneration < 0 ||
		snapshot.StartingCheckpointID != nil && !validSkillUUID(*snapshot.StartingCheckpointID) || snapshot.SystemReleases == nil || snapshot.Items == nil {
		return errors.New("skill snapshot binding or metadata is invalid")
	}
	digest, err := Digest(snapshot.Manifest)
	if err != nil || digest != snapshot.TreeDigest {
		return errors.New("skill snapshot manifest identity is invalid")
	}
	paths := make(map[string]bool, len(snapshot.Manifest.Entries))
	for _, entry := range snapshot.Manifest.Entries {
		paths[entry.Path] = true
	}
	names, states := make(map[string]bool), make(map[string]bool)
	for _, item := range snapshot.Items {
		if !validSkillUUID(item.StateID) || !validSkillUUID(item.CheckpointID) || item.StateEpoch < 1 ||
			!snapshotSkillName.MatchString(item.EntryName) || item.EntryName == "ego-browser" || item.EntryName == "agent-remote-device" || !paths[item.EntryName] || names[item.EntryName] || states[item.StateID] ||
			!validSkillUUID(item.Resolution.RevisionID) || !item.Resolution.Enabled || !item.Resolution.Eligible || !item.Resolution.Included || item.Resolution.ExclusionReason != nil ||
			!validSnapshotRuleSource(item.Resolution.EnabledSource) || !validSnapshotRuleSource(item.Resolution.RevisionSource) {
			return errors.New("invalid skill snapshot member")
		}
		names[item.EntryName], states[item.StateID] = true, true
	}
	return nil
}

func validSnapshotRuleSource(source string) bool {
	return source == "user" || source == "tool" || source == "account"
}

func validSkillUUID(value string) bool {
	return snapshotInputUUID.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

// InputDigest binds all original members, system references and ownership to one preparation.
// Object key order is insignificant; integer values never pass through floating-point decoding.
func (snapshot SkillSnapshot) InputDigest() (string, error) {
	if err := snapshot.Validate(snapshot.SkillSnapshotIdentity); err != nil {
		return "", err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}
