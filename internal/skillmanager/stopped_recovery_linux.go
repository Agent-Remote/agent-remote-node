package skillmanager

import (
	"context"
	"errors"
	"io"
	"os"
)

// StoppedRecoveryExport binds bounded recovery scans to an original retained unfrozen source.
// The privileged caller must hold lifecycle exclusion and independently prove writer quiescence.
// This read handle is not a persisted snapshot, launch authority or reclamation receipt.
type StoppedRecoveryExport struct {
	source      *StoppedWorkExport
	observation RecoveryObservation
}

// OpenStoppedWorkRecovery observes the complete original tree without changing runtime quotas.
// It shares the exact retained binding, termination and descriptor checks with v1 stopped export.
func OpenStoppedWorkRecovery(ctx context.Context, bundle *os.Root, session SessionSnapshot) (*StoppedRecoveryExport, error) {
	source, err := openStoppedWorkSource(ctx, bundle, session)
	if err != nil {
		return nil, err
	}
	observed, err := WalkRecoveryTree(ctx, source.work, source.baseline, source.options, nil)
	if err == nil {
		err = source.verifyRetainedSource(ctx)
	}
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	return &StoppedRecoveryExport{source: source, observation: observed}, nil
}

// Close releases only source descriptors; the caller retains ownership of the bundle descriptor.
func (s *StoppedRecoveryExport) Close() error { return s.source.Close() }

// Observation returns provisional complete metadata, including private local verification evidence.
func (s *StoppedRecoveryExport) Observation() RecoveryObservation { return s.observation }

// Unclean retains the original classification; absent termination evidence cannot become clean.
func (s *StoppedRecoveryExport) Unclean() bool { return s.source.Unclean() }

// Walk visits every entry and verifies complete file consumption against its observed hash and type.
// Readers are callback-scoped and must not escape. Non-files have a nil reader. All emitted bytes
// remain provisional until Walk, a subsequent Verify and independent writer checks have succeeded.
func (s *StoppedRecoveryExport) Walk(ctx context.Context, visit func(Entry, io.Reader) error) error {
	if visit == nil {
		return errors.New("missing recovery visitor")
	}
	return s.compare(ctx, func(entry Entry) error {
		if entry.Kind != "file" {
			return visit(entry, nil)
		}
		file, err := openRetainedSourceFile(s.source.work, entry.Path)
		if err != nil {
			return err
		}
		defer file.Close()
		verifier, err := NewContentVerifier(entry)
		if err != nil {
			return err
		}
		// A consumer cannot bless omitted bytes. It must read the whole declared object itself.
		if err := visit(entry, io.TeeReader(file, verifier)); err != nil {
			return err
		}
		var extra [1]byte
		if n, err := file.Read(extra[:]); n != 0 || err != io.EOF {
			return errors.New("recovery visitor did not consume an exact complete file")
		}
		return verifier.Finish()
	})
}

// Verify repeats the complete read observation and retained-source checks before stream completion.
func (s *StoppedRecoveryExport) Verify(ctx context.Context) error { return s.compare(ctx, nil) }

// Stream hashes and transmits each file in one pass, so large files do not create silent prehash gaps.
// All sink output remains provisional until final whole-source and independent writer verification.
func (s *StoppedRecoveryExport) Stream(ctx context.Context, sink RecoverySink) error {
	if sink == nil {
		return errors.New("missing recovery sink")
	}
	if err := s.source.verifyRetainedSource(ctx); err != nil {
		return err
	}
	observed, err := walkRecoveryTree(ctx, s.source.work, s.source.baseline, s.source.options, nil, sink)
	return s.verifyObservation(ctx, observed, err)
}

func (s *StoppedRecoveryExport) compare(ctx context.Context, visit func(Entry) error) error {
	if err := s.source.verifyRetainedSource(ctx); err != nil {
		return err
	}
	observed, err := WalkRecoveryTree(ctx, s.source.work, s.source.baseline, s.source.options, visit)
	return s.verifyObservation(ctx, observed, err)
}

func (s *StoppedRecoveryExport) verifyObservation(ctx context.Context, observed RecoveryObservation, err error) error {
	if err != nil {
		return err
	}
	if observed != s.observation {
		return errors.New("stopped recovery source changed between complete passes")
	}
	return s.source.verifyRetainedSource(ctx)
}
