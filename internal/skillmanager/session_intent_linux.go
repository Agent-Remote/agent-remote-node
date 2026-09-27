package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

// SessionSpecIntent binds declarative spec creation to one original task, snapshot and launch input.
// Only digests and identities are retained; broker nonces and launch argument bytes are not stored.
type SessionSpecIntent struct {
	Version     int                   `json:"version"`
	Identity    SkillSnapshotIdentity `json:"identity"`
	InputDigest string                `json:"input_digest"`
	SpecDigest  string                `json:"spec_digest"`
	State       string                `json:"state"`
}

func validateSessionSpecIntent(record SessionSpecIntent) error {
	if record.Identity.Validate() != nil || record.Identity.RuntimeBackend != "native" || record.Version != 1 || !contentDigestPattern.MatchString(record.InputDigest) {
		return errors.New("invalid managed session spec intent")
	}
	if record.State == "started" && record.SpecDigest == "" || record.State == "ready" && contentDigestPattern.MatchString(record.SpecDigest) {
		return nil
	}
	return errors.New("invalid managed session spec phase")
}

// ReadSessionSpecIntent returns the original phase only when all immutable inputs match.
func ReadSessionSpecIntent(root *os.Root, expected SessionSpecIntent) (SessionSpecIntent, error) {
	if err := validateSessionSpecIntent(expected); err != nil {
		return SessionSpecIntent{}, err
	}
	var saved SessionSpecIntent
	if err := readPrivateJSON(root, "spec-intent-"+expected.Identity.SessionID+".json", 1<<20, &saved); err != nil {
		return saved, err
	}
	if err := validateSessionSpecIntent(saved); err != nil {
		return saved, err
	}
	identity := saved
	identity.State, identity.SpecDigest = expected.State, expected.SpecDigest
	if identity != expected {
		return saved, errors.New("managed session spec input conflicts with its original intent")
	}
	return saved, nil
}

// BeginSessionSpecIntent records immutable intent before any session directory is created.
func BeginSessionSpecIntent(root *os.Root, record SessionSpecIntent) error {
	if err := validateSessionSpecIntent(record); err != nil {
		return err
	}
	if record.State != "started" {
		return errors.New("managed session spec must begin in started state")
	}
	directory, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return writePrivateJSON(root, directory, "spec-intent-"+record.Identity.SessionID+".json", record, true)
}

// FinishSessionSpecIntent seals the original spec digest after durable publication and ACL setup.
func FinishSessionSpecIntent(root *os.Root, expected SessionSpecIntent, digest string) error {
	if !contentDigestPattern.MatchString(digest) {
		return errors.New("invalid managed session spec digest")
	}
	saved, err := ReadSessionSpecIntent(root, expected)
	if err != nil {
		return err
	}
	draft, err := ReadSessionSpecDraft(root, expected)
	if err != nil {
		return err
	}
	draftDigest := sha256.Sum256(draft)
	if hex.EncodeToString(draftDigest[:]) != digest {
		return errors.New("managed session spec receipt differs from its original draft")
	}
	if saved.State == "ready" {
		if saved.SpecDigest != digest {
			return errors.New("managed session spec receipt cannot change")
		}
		return nil
	}
	saved.State, saved.SpecDigest = "ready", digest
	directory, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return writePrivateJSON(root, directory, "spec-intent-"+saved.Identity.SessionID+".json", saved, false)
}
