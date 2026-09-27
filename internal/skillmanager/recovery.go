package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"math"
	"strconv"
)

// RecoveryObservation identifies a complete read pass, not a manifest or durable checkpoint.
// SourceDigest is private inode evidence; neither digest authorizes publication or deletion.
type RecoveryObservation struct {
	Entries      int64
	FileBytes    int64
	FileObjects  int64
	TreeDigest   string
	SourceDigest string
}

// RecoverySink consumes provisional entries in filesystem enumeration order.
// Begin receives file size but no hash/classification, which are learned while writing its bytes.
// Non-files require a nil writer. End receives complete entry metadata after local verification.
// No entry or byte is complete recovery evidence until the caller verifies the whole source.
type RecoverySink interface {
	Begin(Entry) (io.Writer, error)
	End(Entry) error
}

// RecoveryHasher checks entry-local invariants and hashes ordered recovery metadata incrementally.
// It does not prove parent/link topology, path uniqueness, source authority or actual file bytes.
type RecoveryHasher struct {
	digest  hash.Hash
	entries int64
	bytes   int64
	files   int64
}

// NewRecoveryHasher starts the recovery-specific digest domain, independent of manifest v1.
func NewRecoveryHasher() *RecoveryHasher {
	digest := sha256.New()
	writeField(digest, "agent-remote-skill-recovery-tree-v1")
	return &RecoveryHasher{digest: digest}
}

// Add includes one complete entry after structural validation and signed counter checks.
func (h *RecoveryHasher) Add(entry Entry) error {
	if err := validateEntry(entry); err != nil {
		return err
	}
	if h.entries == math.MaxInt64 || entry.Size > math.MaxInt64-h.bytes {
		return errors.New("recovery counters overflow the protocol")
	}
	h.entries++
	h.bytes += entry.Size
	if entry.Kind == "file" {
		h.files++
	}
	for _, field := range []string{entry.Path, entry.Kind, strconv.FormatUint(uint64(entry.Mode), 10),
		strconv.FormatInt(entry.Size, 10), entry.SHA256, entry.Target, entry.ContentKind, entry.Dependency} {
		writeField(h.digest, field)
	}
	return nil
}

// Observation returns ordered content evidence without inventing source inode attestation.
func (h *RecoveryHasher) Observation() RecoveryObservation {
	return RecoveryObservation{Entries: h.entries, FileBytes: h.bytes, FileObjects: h.files,
		TreeDigest: hex.EncodeToString(h.digest.Sum(nil))}
}

// ValidateRecoveryStart checks provisional entry metadata before any content is read or framed.
// File hash/classification must be absent and appear only after complete content verification.
func ValidateRecoveryStart(entry Entry) error {
	if entry.Kind == "file" {
		if entry.SHA256 != "" || entry.ContentKind != "" {
			return errors.New("recovery file start already claims content verification")
		}
		// Reuse all normal file/path/size invariants with locally supplied shape-only fields.
		entry.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		entry.ContentKind = "binary"
	}
	return validateEntry(entry)
}
