package skillmanager

import (
	"context"
	"errors"
	"os"
)

// RetainFinalizationReclaimed records completion only after both exact content roots are absent.
// It never performs deletion. The serialized caller must retain writer/mount/reference exclusion
// through deletion and this call, preserving every audit record and the original intent.
func RetainFinalizationReclaimed(ctx context.Context, bundle *os.Root, expected FinalizationReclamation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	intent, _, err := ReadFinalizationReclamation(bundle, expected.Capture.Binding)
	if err != nil {
		return err
	}
	if intent == nil || !sameReclamationIntent(*intent, expected) {
		return errors.New("reclamation completion requires exact retained intent")
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return err
	}
	defer journal.Close()
	for _, target := range []struct {
		parent *os.Root
		name   string
	}{{bundle, "work"}, {journal, "objects"}} {
		if _, err := target.parent.Lstat(target.name); !errors.Is(err, os.ErrNotExist) {
			return errors.New("reclamation completion requires absent content roots")
		}
		if err := syncDirectory(target.parent, "."); err != nil {
			return err
		}
	}
	directory, err := privateBundleFile(journal)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	var completed FinalizationReclamation
	if err := readPrivateJSON(journal, "reclaimed.json", 1<<20, &completed); err == nil {
		if !sameReclamationIntent(completed, *intent) {
			return errors.New("reclamation completion changed original intent")
		}
		return directory.Sync()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writePrivateJSON(journal, directory, "reclaimed.json", *intent, true)
}

func sameReclamationIntent(first, second FinalizationReclamation) bool {
	if !sameReclamationAuthorization(first.Authorization, second.Authorization) {
		return false
	}
	first.Authorization, second.Authorization = ReclamationAuthorization{}, ReclamationAuthorization{}
	return first == second
}

func reclamationContentError(bundle, journal *os.Root, record FinalizationRecord) error {
	intent, complete, err := readReclamationIntent(bundle, journal, record)
	if err != nil {
		return err
	}
	if complete {
		return ErrFinalizationReclaimed
	}
	if intent != nil {
		return ErrFinalizationReclaiming
	}
	return nil
}

func requireUnreclaimedWork(bundle *os.Root) error {
	if _, err := bundle.Lstat("finalization"); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	directory, err := openPrivateRetainedDirectory(bundle, "finalization")
	if err != nil {
		return err
	}
	defer directory.Close()
	// A content opener cannot turn a corrupt marker into permission to use an old work path.
	for _, name := range []string{"reclamation.json", "reclaimed.json"} {
		file, err := openPrivateRetainedFile(directory, name, -1)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		_ = file.Close()
		return ErrFinalizationReclaiming
	}
	return nil
}
