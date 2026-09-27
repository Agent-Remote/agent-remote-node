package skillexport

import (
	"context"
	"io"
	"regexp"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// RecoveryMagic identifies complete streaming recovery metadata, never a manifest v1 snapshot.
const RecoveryMagic = "ARSKRC\x00\x01"

const recoveryEntryBytes = 64 << 10

var recoveryDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// RecoveryHeader binds a complete read observation to the original authorized snapshot identity.
type RecoveryHeader struct {
	Version        int                            `json:"version"`
	Binding        skillmanager.NodeExportBinding `json:"binding"`
	RecoveryDigest string                         `json:"recovery_digest"`
	Unclean        bool                           `json:"unclean"`
	Entries        int64                          `json:"entries"`
	FileBytes      int64                          `json:"file_bytes"`
	FileObjects    int64                          `json:"file_objects"`
}

type recoveryCompletion struct {
	Version        int    `json:"version"`
	RecoveryDigest string `json:"recovery_digest"`
	Entries        int64  `json:"entries"`
	FileBytes      int64  `json:"file_bytes"`
	FileObjects    int64  `json:"file_objects"`
	Complete       bool   `json:"complete"`
}

type recoveryContent struct {
	SHA256      string `json:"sha256"`
	ContentKind string `json:"content_kind"`
}

// RecoverySource holds independently proven stopped original work for complete read-only passes.
// Stream and Verify must compare full content and source-identity observations with the initial scan.
type RecoverySource interface {
	Observation() skillmanager.RecoveryObservation
	Unclean() bool
	Stream(context.Context, skillmanager.RecoverySink) error
	Verify(context.Context) error
}

// RecoveryStore supplies only the explicitly negotiated complete recovery format through Helper.
type RecoveryStore interface {
	StreamStoppedSkillRecovery(context.Context, string, skillmanager.NodeExportBinding, io.Writer, func(Header, bool) error) error
}

// WriteRecovery sends entry metadata before file bytes and content claims only after hashing them.
// verify owns the bounded final source scan and independent writer rechecks. No footer precedes it.
func WriteRecovery(ctx context.Context, output io.Writer, binding skillmanager.NodeExportBinding, source RecoverySource, verify func(context.Context) error) error {
	observed := source.Observation()
	header := RecoveryHeader{1, binding, observed.TreeDigest, source.Unclean(), observed.Entries, observed.FileBytes, observed.FileObjects}
	if header.validate() != nil || verify == nil {
		return ErrUnavailable
	}
	if _, err := io.WriteString(output, RecoveryMagic); err != nil {
		return err
	}
	if err := writeFrame(output, header, 4096); err != nil {
		return err
	}
	sink := &recoveryWriter{ctx: ctx, output: output, hasher: skillmanager.NewRecoveryHasher(), header: header}
	if err := source.Stream(ctx, sink); err != nil {
		return err
	}
	if sink.current != nil || !header.matches(sink.hasher.Observation()) {
		return ErrUnavailable
	}
	if err := verify(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeFrame(output, header.completion(), 4096)
}

func (h RecoveryHeader) validate() error {
	if h.Version != 1 || h.Binding.Validate() != nil || !recoveryDigestPattern.MatchString(h.RecoveryDigest) ||
		h.Entries < 0 || h.FileBytes < 0 || h.FileObjects < 0 || h.FileObjects > h.Entries || h.FileObjects == 0 && h.FileBytes != 0 {
		return ErrUnavailable
	}
	return nil
}

func (h RecoveryHeader) observation() Header {
	return Header{Binding: h.Binding, TreeDigest: h.RecoveryDigest, Unclean: h.Unclean}
}

func (h RecoveryHeader) matches(o skillmanager.RecoveryObservation) bool {
	return h.RecoveryDigest == o.TreeDigest && h.Entries == o.Entries && h.FileBytes == o.FileBytes && h.FileObjects == o.FileObjects
}

func (h RecoveryHeader) completion() recoveryCompletion {
	return recoveryCompletion{1, h.RecoveryDigest, h.Entries, h.FileBytes, h.FileObjects, true}
}

type recoveryWriter struct {
	ctx      context.Context
	output   io.Writer
	hasher   *skillmanager.RecoveryHasher
	header   RecoveryHeader
	current  *skillmanager.Entry
	verifier *skillmanager.RecoveryFileVerifier
}

func (w *recoveryWriter) Begin(entry skillmanager.Entry) (io.Writer, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	seen := w.hasher.Observation()
	if w.current != nil || skillmanager.ValidateRecoveryStart(entry) != nil || seen.Entries >= w.header.Entries || entry.Size > w.header.FileBytes-seen.FileBytes {
		return nil, ErrUnavailable
	}
	w.current = &entry
	if err := writeFrame(w.output, entry, recoveryEntryBytes); err != nil {
		return nil, err
	}
	if entry.Kind != "file" {
		return nil, nil
	}
	var err error
	w.verifier, err = skillmanager.NewRecoveryFileVerifier(entry)
	if err != nil {
		return nil, err
	}
	return io.MultiWriter(w.verifier, w.output), nil
}

func (w *recoveryWriter) End(entry skillmanager.Entry) error {
	if w.current == nil {
		return ErrUnavailable
	}
	if entry.Kind == "file" {
		if w.verifier == nil {
			return ErrUnavailable
		}
		actual, err := w.verifier.Complete(entry.SHA256, entry.ContentKind)
		if err != nil || actual != entry {
			return ErrUnavailable
		}
		if err := writeFrame(w.output, recoveryContent{entry.SHA256, entry.ContentKind}, 4096); err != nil {
			return err
		}
	} else if entry != *w.current {
		return ErrUnavailable
	}
	if err := w.hasher.Add(entry); err != nil {
		return err
	}
	w.current, w.verifier = nil, nil
	return w.ctx.Err()
}
