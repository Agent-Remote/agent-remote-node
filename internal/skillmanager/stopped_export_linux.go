package skillmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
)

// StoppedWorkExport holds a read-only tree observation, never a durability or cleanup receipt.
// The privileged caller must own lifecycle exclusion and prove all original writers exited
// before opening it and again before accepting Verify. No descriptor may escape to a worker.
type StoppedWorkExport struct {
	bundle      *os.Root
	work        *os.Root
	session     SessionSnapshot
	baseline    PermissionBaseline
	termination *TerminationRecord
	options     CaptureOptions
	manifest    Manifest
	digest      string
	workInfo    os.FileInfo
	files       map[string]Entry
}

// OpenStoppedWorkExport reads an unfrozen original tree without disk admission or content fsync.
// Explicit export bounds replace runtime quotas so quota overflow does not truncate recovery.
func OpenStoppedWorkExport(ctx context.Context, bundle *os.Root, session SessionSnapshot, maxBytes int64, maxEntries int) (*StoppedWorkExport, error) {
	value, err := openStoppedWorkSource(ctx, bundle, session)
	if err != nil {
		return nil, err
	}
	value.options.DirectoryBytes, value.options.Entries = maxBytes, maxEntries
	value.manifest, err = captureWorkTree(ctx, value.work, value.baseline, value.options, false)
	if err == nil {
		value.digest, err = Digest(value.manifest)
	}
	if err == nil {
		err = unchangedAt(bundle, "work", value.workInfo)
	}
	if err != nil {
		_ = value.Close()
		return nil, err
	}
	value.files = make(map[string]Entry)
	for _, entry := range value.manifest.Entries {
		if entry.Kind == "file" {
			value.files[entry.Path] = entry
		}
	}
	return value, nil
}

func openStoppedWorkSource(ctx context.Context, bundle *os.Root, session SessionSnapshot) (*StoppedWorkExport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := requireUnfrozenWork(bundle); err != nil {
		return nil, err
	}
	termination, err := readExportTermination(bundle, session.Snapshot.Binding)
	if err != nil {
		return nil, err
	}
	retained, err := readSessionSnapshot(bundle, session.Snapshot.Binding.SessionID)
	if err != nil || !reflect.DeepEqual(retained, session) {
		return nil, errors.New("stopped export binding changed")
	}
	before, err := bundle.Lstat("work")
	if err != nil || !before.IsDir() {
		return nil, errors.New("stopped export work is not an original directory")
	}
	file, err := OpenSessionWork(bundle, session.Runtime)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	if !sameSourceInfo(before, info) {
		return nil, errors.New("stopped export work changed before open")
	}
	work, err := bundle.OpenRoot("work")
	if err != nil {
		return nil, err
	}
	value := &StoppedWorkExport{bundle: bundle, work: work, session: session, termination: termination,
		options: session.Snapshot.Capture, workInfo: info}
	err = unchangedAt(work, ".", info)
	if err == nil {
		err = readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &value.baseline)
	}
	if err == nil {
		err = unchangedAt(bundle, "work", info)
	}
	if err != nil {
		_ = work.Close()
		return nil, err
	}
	return value, nil
}

// Close releases only the read descriptor; the caller owns the original bundle descriptor.
func (s *StoppedWorkExport) Close() error { return s.work.Close() }

// Manifest returns detached metadata for framing, not a persisted capture.
func (s *StoppedWorkExport) Manifest() Manifest {
	return Manifest{Version: s.manifest.Version, Entries: append([]Entry{}, s.manifest.Entries...)}
}

// Unclean preserves retained classification; absent evidence can only yield an unclean observation.
func (s *StoppedWorkExport) Unclean() bool { return s.termination == nil || s.termination.Unclean }

// Open reads only a file named by this complete observation through no-follow descriptors.
// The caller must verify exact length, digest and classification before marking an object complete.
func (s *StoppedWorkExport) Open(ctx context.Context, expected Entry) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if entry, found := s.files[expected.Path]; found && entry == expected {
		return openRetainedSourceFile(s.work, entry.Path)
	}
	return nil, errors.New("stopped export object is not in the observed tree")
}

// Verify rechecks all retained authority and the complete source before stream completion.
func (s *StoppedWorkExport) Verify(ctx context.Context) error {
	if err := s.verifyRetainedSource(ctx); err != nil {
		return err
	}
	manifest, err := captureWorkTree(ctx, s.work, s.baseline, s.options, false)
	if err != nil {
		return err
	}
	digest, err := Digest(manifest)
	if err != nil || digest != s.digest {
		return errors.New("stopped export tree changed")
	}
	return ctx.Err()
}

func (s *StoppedWorkExport) verifyRetainedSource(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireUnfrozenWork(s.bundle); err != nil {
		return err
	}
	session, err := readSessionSnapshot(s.bundle, s.session.Snapshot.Binding.SessionID)
	if err != nil || !reflect.DeepEqual(session, s.session) {
		return errors.New("stopped export binding changed")
	}
	termination, err := readExportTermination(s.bundle, s.session.Snapshot.Binding)
	if err != nil || !reflect.DeepEqual(termination, s.termination) {
		return errors.New("stopped export termination changed")
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(s.bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return err
	}
	if !reflect.DeepEqual(baseline, s.baseline) {
		return errors.New("stopped export baseline changed")
	}
	if err := unchangedAt(s.bundle, "work", s.workInfo); err != nil {
		return err
	}
	return ctx.Err()
}

func requireUnfrozenWork(bundle *os.Root) error {
	if _, err := bundle.Lstat("finalization"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("stopped export requires absent finalization")
	}
	return nil
}

func readExportTermination(bundle *os.Root, binding SnapshotBinding) (*TerminationRecord, error) {
	if _, err := bundle.Lstat("termination.json"); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	record, err := ReadTermination(bundle, binding)
	return &record, err
}
