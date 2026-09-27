// Package skillmanager validates and persists private, versioned skill content.
package skillmanager

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"strconv"
	"unicode/utf8"
)

// Entry describes a file, directory or link in the canonical v1 tree.
type Entry struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Mode        uint32 `json:"mode"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Target      string `json:"target"`
	ContentKind string `json:"content_kind"`
	Dependency  string `json:"dependency"`
}

// Manifest is the complete ordered tree; its zero value is not a wire manifest.
type Manifest struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// MarshalJSON emits an empty entry array consistently with the Python and Rust codecs.
func (m Manifest) MarshalJSON() ([]byte, error) {
	if err := Validate(m); err != nil {
		return nil, err
	}
	type plain Manifest
	value := plain(m)
	if value.Entries == nil {
		value.Entries = []Entry{}
	}
	return json.Marshal(value)
}

// UnmarshalJSON preserves the wire defaults and rejects Go's permissive null handling.
func (e *Entry) UnmarshalJSON(data []byte) error {
	if err := validateJSONObject(data, "path", "kind", "mode", "size", "sha256", "target", "content_kind", "dependency"); err != nil {
		return err
	}
	type plain Entry
	value := plain{Mode: 0o644}
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("invalid skill entry field type")
	}
	*e = Entry(value)
	return nil
}

// UnmarshalJSON rejects noncanonical field names, nulls and unsupported field types.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	if err := validateJSONObject(data, "version", "entries"); err != nil {
		return err
	}
	type plain Manifest
	value := plain{Version: 1, Entries: []Entry{}}
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("invalid skill manifest field type")
	}
	*m = Manifest(value)
	return nil
}

// DecodeManifest decodes and validates a complete manifest without touching host paths.
func DecodeManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Digest returns the canonical cross-language tree identity after structural validation.
func Digest(manifest Manifest) (string, error) {
	if err := Validate(manifest); err != nil {
		return "", err
	}
	digest := sha256.New()
	writeField(digest, "agent-remote-skill-tree-v1")
	for _, entry := range manifest.Entries {
		for _, field := range []string{
			entry.Path, entry.Kind, strconv.FormatUint(uint64(entry.Mode), 10),
			strconv.FormatInt(entry.Size, 10), entry.SHA256, entry.Target,
			entry.ContentKind, entry.Dependency,
		} {
			writeField(digest, field)
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// VerifyContent checks both file identity and the declared UTF-8 classification.
func VerifyContent(entry Entry, content []byte) error {
	if err := validateEntry(entry); err != nil {
		return err
	}
	if entry.Kind != "file" || entry.Size != int64(len(content)) {
		return errors.New("skill content size or entry kind does not match")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != entry.SHA256 {
		return errors.New("skill content digest does not match")
	}
	isText := utf8.Valid(content) && !bytes.ContainsRune(content, 0)
	if (entry.ContentKind == "text") != isText {
		return errors.New("skill content classification does not match")
	}
	return nil
}

func writeField(digest hash.Hash, value string) {
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}
