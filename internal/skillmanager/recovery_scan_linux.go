package skillmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// WalkRecoveryTree reads an entire quiescent tree without an entry-count quota or content copy.
// Metadata memory is bounded by the original v1 baseline, depth 128 and 256 names per directory.
// The caller must independently exclude writers and compare complete initial/transfer/final passes.
// A visit receives provisional metadata; neither callbacks nor one successful pass prove stability
// of previously visited siblings. This primitive does not authorize export or create a manifest.
func WalkRecoveryTree(ctx context.Context, work *os.Root, baseline PermissionBaseline, options CaptureOptions, visit func(Entry) error) (RecoveryObservation, error) {
	return walkRecoveryTree(ctx, work, baseline, options, visit, nil)
}

func walkRecoveryTree(ctx context.Context, work *os.Root, baseline PermissionBaseline, options CaptureOptions, visit func(Entry) error, sink RecoverySink) (RecoveryObservation, error) {
	if err := ctx.Err(); err != nil {
		return RecoveryObservation{}, err
	}
	// Only source capture admits runtime quotas. Recovery still validates all sealed exclusions
	// and dependencies, and cannot change the complete-manifest v1 entry limit.
	options.DirectoryBytes, options.Entries = math.MaxInt64, 100_000
	exclusions, dependencies, err := validateCaptureOptions(options)
	if err != nil {
		return RecoveryObservation{}, err
	}
	if _, err := RestoreSourceModes(baseline.Materialized, baseline); err != nil {
		return RecoveryObservation{}, err
	}
	if work == nil {
		return RecoveryObservation{}, errors.New("missing recovery work root")
	}
	scan := recoveryScan{
		work: work, baseline: baseline, visit: visit, sink: sink, tree: NewRecoveryHasher(), source: sha256.New(),
		capture: treeCapture{options: options, exclusions: exclusions, dependencies: dependencies},
	}
	writeField(scan.source, "agent-remote-skill-recovery-source-v1")
	if err := scan.walk(ctx, work, "", 0); err != nil {
		return RecoveryObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return RecoveryObservation{}, err
	}
	observed := scan.tree.Observation()
	observed.SourceDigest = hex.EncodeToString(scan.source.Sum(nil))
	return observed, nil
}

type recoveryScan struct {
	work     *os.Root
	baseline PermissionBaseline
	visit    func(Entry) error
	sink     RecoverySink
	capture  treeCapture
	tree     *RecoveryHasher
	source   hash.Hash
}

func (s *recoveryScan) walk(ctx context.Context, root *os.Root, prefix string, depth int) error {
	if depth > 128 {
		return errors.New("portability_error: recovery tree exceeds supported directory depth")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := root.Lstat(".")
	if err != nil {
		return err
	}
	if err := s.observeSource(prefix, before); err != nil {
		return err
	}
	directory, err := root.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		names, readErr := directory.Readdirnames(256)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			if prefix == "" && s.capture.exclusions[name] {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "/" + name
			}
			if err := s.walkEntry(ctx, root, name, path, depth); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return unchangedAt(root, ".", before)
}

func (s *recoveryScan) walkEntry(ctx context.Context, root *os.Root, name, path string, depth int) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	if path == "ego-browser" || path == "agent-remote-device" {
		return errors.New("portability_error: undeclared reserved recovery entry")
	}
	before, err := root.Lstat(name)
	if err != nil {
		return err
	}
	entry, err := recoveryMetadata(root, name, path, before, s.capture.dependencies)
	if err != nil {
		return err
	}
	if entry.Kind == "symlink" {
		err = validateLinkLookup(entry, func(path string) (Entry, error) { return s.lookup(ctx, path) })
	}
	if err != nil {
		return err
	}
	// The preparation baseline is sorted and bounded independently of the recovered tree.
	index := sort.Search(len(s.baseline.Source.Entries), func(i int) bool { return s.baseline.Source.Entries[i].Path >= path })
	if index < len(s.baseline.Source.Entries) && s.baseline.Source.Entries[index].Path == path {
		actual := s.baseline.Materialized.Entries[index]
		if actual.Kind == entry.Kind && actual.Mode == entry.Mode {
			entry.Mode = s.baseline.Source.Entries[index].Mode
		}
	}
	if entry.Kind == "file" {
		entry.Size = before.Size()
	}
	if err := ValidateRecoveryStart(entry); err != nil {
		return err
	}
	var output io.Writer
	if s.sink != nil {
		output, err = s.sink.Begin(entry)
		if err != nil {
			return err
		}
		if (output == nil) != (entry.Kind != "file") {
			return errors.New("recovery sink returned an invalid content writer")
		}
	}
	if entry.Kind == "file" {
		entry, err = s.capture.captureFileTo(ctx, root, name, entry, before, output)
		if err != nil {
			return err
		}
	}
	if err := s.tree.Add(entry); err != nil {
		return err
	}
	if s.sink != nil {
		if err := s.sink.End(entry); err != nil {
			return err
		}
	}
	if err := s.observeSource(path, before); err != nil {
		return err
	}
	if s.visit != nil {
		if err := s.visit(entry); err != nil {
			return err
		}
	}
	if entry.Kind == "directory" {
		child, err := root.OpenRoot(name)
		if err != nil {
			return err
		}
		err = unchangedAt(child, ".", before)
		if err == nil {
			err = s.walk(ctx, child, path, depth+1)
		}
		_ = child.Close()
		if err != nil {
			return err
		}
	}
	return unchangedAt(root, name, before)
}

func (s *recoveryScan) observeSource(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("recovery source has unknown inode metadata")
	}
	writeField(s.source, path)
	for _, value := range []uint64{uint64(stat.Dev), stat.Ino, uint64(stat.Mode), uint64(stat.Nlink), uint64(stat.Uid), uint64(stat.Gid)} {
		writeField(s.source, strconv.FormatUint(value, 10))
	}
	for _, value := range []int64{stat.Size, stat.Mtim.Sec, stat.Mtim.Nsec, stat.Ctim.Sec, stat.Ctim.Nsec} {
		writeField(s.source, strconv.FormatInt(value, 10))
	}
	return nil
}
