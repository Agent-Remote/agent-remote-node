package skillmanager

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"unicode/utf8"
)

// ContentVerifier verifies bounded content incrementally without retaining file bytes.
// A verifier belongs to one stream and is not safe for concurrent writes.
type ContentVerifier struct {
	entry      Entry
	digest     hash.Hash
	classifier textClassifier
	count      int64
	failed     bool
}

// NewContentVerifier validates the declared file before accepting bytes.
func NewContentVerifier(entry Entry) (*ContentVerifier, error) {
	if err := validateEntry(entry); err != nil {
		return nil, err
	}
	if entry.Kind != "file" {
		return nil, errors.New("content verifier requires a file")
	}
	return &ContentVerifier{entry: entry, digest: sha256.New()}, nil
}

// Write rejects excess bytes before hashing and retains only incomplete UTF-8 tails.
func (v *ContentVerifier) Write(data []byte) (int, error) {
	if v.failed || int64(len(data)) > v.entry.Size-v.count {
		v.failed = true
		return 0, errors.New("skill content exceeds declared size")
	}
	v.count += int64(len(data))
	_, _ = v.digest.Write(data)
	v.classifier.update(data)
	return len(data), nil
}

// Finish succeeds only after all bytes match their size, digest and text classification.
func (v *ContentVerifier) Finish() error {
	if v.failed || v.count != v.entry.Size || hex.EncodeToString(v.digest.Sum(nil)) != v.entry.SHA256 ||
		(v.entry.ContentKind == "text") != v.classifier.isText() {
		return errors.New("skill content verification failed")
	}
	return nil
}

type textClassifier struct {
	tail   []byte
	binary bool
}

func (c *textClassifier) update(chunk []byte) {
	if c.binary {
		return
	}
	if bytes.IndexByte(chunk, 0) >= 0 {
		c.binary = true
		return
	}
	if len(c.tail) > 0 {
		chunk = append(c.tail, chunk...)
		c.tail = nil
	}
	for len(chunk) > 0 {
		if chunk[0] < utf8.RuneSelf {
			chunk = chunk[1:]
			continue
		}
		if !utf8.FullRune(chunk) {
			c.tail = append([]byte{}, chunk...)
			return
		}
		value, size := utf8.DecodeRune(chunk)
		if value == utf8.RuneError && size == 1 {
			c.binary = true
			return
		}
		chunk = chunk[size:]
	}
}

func (c *textClassifier) isText() bool {
	return !c.binary && len(c.tail) == 0
}
