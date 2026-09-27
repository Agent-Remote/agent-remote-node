package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
)

// Only runtimehelper interprets this opaque spec's typed host bindings. The private draft
// permits recovery between publishing a spec and sealing its receipt; nonces are excluded.
type sessionSpecDraft struct {
	Version     int             `json:"version"`
	InputDigest string          `json:"input_digest"`
	SpecDigest  string          `json:"spec_digest"`
	Spec        json.RawMessage `json:"spec"`
}

// ReadSessionSpecDraft checks the private draft's exact original input and complete byte digest.
func ReadSessionSpecDraft(root *os.Root, expected SessionSpecIntent) ([]byte, error) {
	intent, err := ReadSessionSpecIntent(root, expected)
	if err != nil {
		return nil, err
	}
	var draft sessionSpecDraft
	if err := readPrivateJSON(root, "spec-draft-"+expected.Identity.SessionID+".json", 256<<10, &draft); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(draft.Spec)
	if draft.Version != 1 || draft.InputDigest != expected.InputDigest || len(draft.Spec) == 0 || len(draft.Spec) > 128<<10 || !json.Valid(draft.Spec) || draft.SpecDigest != hex.EncodeToString(digest[:]) || intent.State == "ready" && intent.SpecDigest != draft.SpecDigest {
		return nil, errors.New("managed session spec draft is inconsistent")
	}
	return draft.Spec, nil
}

// PublishSessionSpecDraft retains a Helper-generated, nonce-free spec before runtime publication.
func PublishSessionSpecDraft(root *os.Root, expected SessionSpecIntent, data []byte) error {
	if len(data) == 0 || len(data) > 128<<10 || !json.Valid(data) {
		return errors.New("invalid managed session spec draft")
	}
	// Match encoding/json's representation inside the private envelope, including
	// whitespace and HTML escaping, before computing the retained byte digest.
	data, err := json.Marshal(json.RawMessage(data))
	if err != nil {
		return err
	}
	intent, err := ReadSessionSpecIntent(root, expected)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if old, err := ReadSessionSpecDraft(root, expected); err == nil {
		if sha256.Sum256(old) != digest {
			return errors.New("managed session spec draft cannot be replaced")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if intent.State != "started" {
		return errors.New("ready managed session spec lost its draft")
	}
	directory, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	draft := sessionSpecDraft{Version: 1, InputDigest: expected.InputDigest, SpecDigest: hex.EncodeToString(digest[:]), Spec: data}
	return writePrivateJSON(root, directory, "spec-draft-"+expected.Identity.SessionID+".json", draft, true)
}
