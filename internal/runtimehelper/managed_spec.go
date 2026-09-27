package runtimehelper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

const managedSpecOperation = "prepare_managed_session_spec"

// ManagedSessionSpecBinding binds a trusted spec to the original snapshot and immutable creation input.
type ManagedSessionSpecBinding struct {
	TaskID              string `json:"task_id"`
	SnapshotInputDigest string `json:"snapshot_input_digest"`
	RequestDigest       string `json:"request_digest"`
}

// ManagedSessionSpecRequest requests Native spec creation without choosing privileged host paths.
// SnapshotInputDigest must come from the freshly authenticated complete original snapshot.
type ManagedSessionSpecRequest struct {
	Snapshot            skillmanager.SkillSnapshotIdentity `json:"snapshot"`
	SnapshotInputDigest string                             `json:"snapshot_input_digest"`
	SystemReleases      skillmanager.SystemReleasePins     `json:"system_releases"`
	Session             toolsessions.CreatePayload         `json:"session"`
}

func (r ManagedSessionSpecRequest) validate(nodeID string) error {
	if r.Snapshot.Validate() != nil || r.Snapshot.RuntimeBackend != "native" || r.Snapshot.NodeID != nodeID || !validLowerHexDigest(r.SnapshotInputDigest) || r.SystemReleases.Validate() != nil {
		return errors.New("invalid managed session spec snapshot")
	}
	s := r.Session
	if s.SessionID != r.Snapshot.SessionID || s.UserID != r.Snapshot.UserID || s.ToolAccountID != r.Snapshot.AccountID || s.RuntimeBackend != "native" || s.ToolType != "claude" || !validSkillUUID(s.WorkspaceID) {
		return errors.New("managed session spec does not match its original owners")
	}
	return nil
}

func managedSpecDigest(spec SessionSpec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func managedSpecInputDigest(requestID string, request ManagedSessionSpecRequest, config EngineConfig) (string, error) {
	// A freshly registered broker nonce can change on retry; it never becomes disk authority.
	request.Session.EgoBrowserBrokerNonce = ""
	data, err := json.Marshal(struct {
		Task    string
		Request ManagedSessionSpecRequest
		Config  EngineConfig
	}{requestID, request, config.WithDefaults()})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// NewManagedSessionSpecRequest binds declarative launch input to an authenticated original snapshot.
func NewManagedSessionSpecRequest(snapshot skillmanager.SkillSnapshot, session toolsessions.CreatePayload) (ManagedSessionSpecRequest, error) {
	if err := snapshot.Validate(snapshot.SkillSnapshotIdentity); err != nil {
		return ManagedSessionSpecRequest{}, err
	}
	digest, err := snapshot.InputDigest()
	if err != nil {
		return ManagedSessionSpecRequest{}, err
	}
	pins, err := parseSkillSystemPins(snapshot.SystemReleases)
	if err != nil {
		return ManagedSessionSpecRequest{}, err
	}
	request := ManagedSessionSpecRequest{Snapshot: snapshot.SkillSnapshotIdentity, SnapshotInputDigest: digest, SystemReleases: pins, Session: session}
	return request, request.validate(snapshot.NodeID)
}
