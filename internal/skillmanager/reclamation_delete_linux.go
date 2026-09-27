package skillmanager

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type reclamationEntry struct {
	entry      Entry
	otherMode  uint32
	allowMode  bool
	ignoreMode bool
}

type reclamationRoot struct {
	parent   *os.File
	file     *os.File
	name     string
	mount    uint64
	original ReclamationDirectory
	entries  map[string]reclamationEntry
	private  bool
}

// ReclamationChecks separates external resource inspection from the per-entry exclusion guard.
// Verify runs before and after deletion phases. Guard runs during traversal and before unlinks.
// The caller must hold lifecycle exclusion throughout both; neither callback may cache permission
// across invocations or substitute for the executor's reader locks and descriptor checks.
type ReclamationChecks struct {
	Verify func(context.Context) error
	Guard  func(context.Context) error
}

// ReclaimFinalizationContent removes only the original two roots named by durable intent.
// The caller must own lifecycle exclusion and supply repeated proof that writers, mounts/aliases
// and local consumers remain absent. No remote deadline is recreated for an existing intent.
func ReclaimFinalizationContent(ctx context.Context, bundle *os.Root, expected FinalizationReclamation, verify func(context.Context) error) error {
	return ReclaimFinalizationContentWithChecks(ctx, bundle, expected, ReclamationChecks{Verify: verify, Guard: verify})
}

// ReclaimFinalizationContentWithChecks applies phase verification and a per-entry lifecycle guard.
// External commands belong in Verify so their number does not grow with the captured file count.
func ReclaimFinalizationContentWithChecks(ctx context.Context, bundle *os.Root, expected FinalizationReclamation, checks ReclamationChecks) error {
	if checks.Verify == nil || checks.Guard == nil {
		return errors.New("reclamation requires independent runtime and reference verification")
	}
	verify := checks.Verify
	if err := ctx.Err(); err != nil {
		return err
	}
	lock, err := lockFinalizationContent(bundle, expected.Capture.Binding, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	intent, complete, err := ReadFinalizationReclamation(bundle, expected.Capture.Binding)
	if err != nil {
		return err
	}
	if intent == nil || !sameReclamationIntent(*intent, expected) {
		return errors.New("reclamation requires exact durable intent")
	}
	if err := verify(ctx); err != nil {
		return err
	}
	if complete {
		return RetainFinalizationReclaimed(ctx, bundle, *intent)
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return err
	}
	defer journal.Close()
	workEntries, objectEntries, err := reclamationEntries(bundle, journal, intent.Capture)
	if err != nil {
		return err
	}
	work, err := openReclamationRoot(bundle, "work", intent.Work, workEntries, false)
	if err != nil {
		return err
	}
	defer work.close()
	objects, err := openReclamationRoot(journal, "objects", intent.Objects, objectEntries, true)
	if err != nil {
		return err
	}
	defer objects.close()
	roots := []*reclamationRoot{work, objects}
	// Inspect both remaining trees before the first unlink. An unexpected survivor must not
	// trigger deletion of otherwise valid siblings in this invocation.
	for _, root := range roots {
		if root.file != nil {
			if err := root.walk(ctx, root.file, "", false, checks.Guard); err != nil {
				return err
			}
		}
	}
	for _, root := range roots {
		if err := verify(ctx); err != nil {
			return err
		}
		if root.file == nil {
			continue
		}
		if err := root.checkOriginal(); err != nil {
			return err
		}
		if err := root.walk(ctx, root.file, "", true, checks.Guard); err != nil {
			return err
		}
		if err := verify(ctx); err != nil {
			return err
		}
		if err := root.checkOriginal(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unix.Unlinkat(int(root.parent.Fd()), root.name, unix.AT_REMOVEDIR); err != nil {
			return err
		}
		if err := root.parent.Sync(); err != nil {
			return err
		}
	}
	if err := verify(ctx); err != nil {
		return err
	}
	return RetainFinalizationReclaimed(ctx, bundle, *intent)
}

func reclamationEntries(bundle, journal *os.Root, capture FinalizationRecord) (map[string]reclamationEntry, map[string]reclamationEntry, error) {
	var manifest Manifest
	if err := readPrivateJSON(journal, "manifest.json", maxManifestBytes, &manifest); err != nil {
		return nil, nil, err
	}
	digest, err := Digest(manifest)
	if err != nil || digest != capture.TreeDigest {
		return nil, nil, errors.New("reclamation manifest changed")
	}
	var baseline PermissionBaseline
	if err := readPrivateJSON(bundle, "baseline.json", maxBaselineBytes, &baseline); err != nil {
		return nil, nil, err
	}
	if _, err := RestoreSourceModes(baseline.Materialized, baseline); err != nil {
		return nil, nil, err
	}
	previous := make(map[string]int, len(baseline.Source.Entries))
	for index, entry := range baseline.Source.Entries {
		previous[entry.Path] = index
	}
	work, objects := make(map[string]reclamationEntry), make(map[string]reclamationEntry)
	for _, entry := range manifest.Entries {
		value := reclamationEntry{entry: entry}
		if index, ok := previous[entry.Path]; ok && baseline.Source.Entries[index].Kind == entry.Kind && baseline.Source.Entries[index].Mode == entry.Mode {
			value.otherMode, value.allowMode = baseline.Materialized.Entries[index].Mode, true
		}
		work[entry.Path] = value
		if entry.Kind == "file" {
			entry.Path, entry.Mode = entry.SHA256, 0600
			if old, ok := objects[entry.Path]; ok && old.entry != entry {
				return nil, nil, errors.New("reclamation object declarations disagree")
			}
			objects[entry.Path] = reclamationEntry{entry: entry}
		}
	}
	snapshot, err := loadPreparedSnapshot(bundle, capture.Binding)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range snapshot.Capture.SystemPaths {
		if _, exists := work[name]; exists {
			return nil, nil, errors.New("reclamation exclusion collides with captured content")
		}
		work[name] = reclamationEntry{entry: Entry{Path: name, Kind: "directory"}, ignoreMode: true}
	}
	return work, objects, nil
}

func openReclamationRoot(parent *os.Root, name string, original ReclamationDirectory, entries map[string]reclamationEntry, private bool) (*reclamationRoot, error) {
	directory, err := privateBundleFile(parent)
	if err != nil {
		return nil, err
	}
	root := &reclamationRoot{parent: directory, name: name, original: original, entries: entries, private: private}
	root.mount, err = reclamationMountID(int(directory.Fd()), "")
	if err == nil {
		fd, openErr := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) {
			return root, nil
		}
		err = openErr
		if err == nil {
			root.file = os.NewFile(uintptr(fd), name)
			err = root.checkOriginal()
		}
	}
	if err != nil {
		root.close()
		return nil, err
	}
	return root, nil
}

func (r *reclamationRoot) close() {
	if r.file != nil {
		_ = r.file.Close()
	}
	_ = r.parent.Close()
}

func (r *reclamationRoot) checkOriginal() error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(r.file.Fd()), &stat); err != nil {
		return err
	}
	if uint64(stat.Dev) != r.original.Device || stat.Ino != r.original.Inode {
		return errors.New("reclamation content root changed")
	}
	if r.private && (stat.Uid != uint32(os.Geteuid()) || stat.Mode&07777 != 0700) {
		return errors.New("reclamation object root lost private ownership")
	}
	return r.checkEntry(int(r.parent.Fd()), r.name, stat)
}

func reclamationMountID(fd int, name string) (uint64, error) {
	var stat unix.Statx_t
	if err := unix.Statx(fd, name, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, err
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, errors.New("reclamation mount identity unavailable")
	}
	return stat.Mnt_id, nil
}

func (r *reclamationRoot) checkEntry(parent int, name string, expected unix.Stat_t) error {
	mount, err := reclamationMountID(parent, name)
	if err != nil {
		return err
	}
	if mount != r.mount {
		return errors.New("reclamation refuses a mounted content entry")
	}
	var actual unix.Stat_t
	if err := unix.Fstatat(parent, name, &actual, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if !sameReclamationStat(actual, expected) {
		return errors.New("reclamation entry changed during verification")
	}
	return nil
}

func sameReclamationStat(first, second unix.Stat_t) bool {
	return first.Dev == second.Dev && first.Ino == second.Ino && first.Mode == second.Mode &&
		first.Uid == second.Uid && first.Gid == second.Gid && first.Size == second.Size &&
		first.Nlink == second.Nlink && first.Mtim == second.Mtim && first.Ctim == second.Ctim
}
