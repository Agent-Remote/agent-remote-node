package skillmanager

import (
	"errors"
	"os"
)

// TerminationRecord preserves proven exit classification across capture retries and restarts.
type TerminationRecord struct {
	Version int             `json:"version"`
	Binding SnapshotBinding `json:"binding"`
	Unclean bool            `json:"unclean"`
}

// ReadTermination reads helper-owned evidence that all writers exited before capture began.
func ReadTermination(bundle *os.Root, binding SnapshotBinding) (TerminationRecord, error) {
	if _, err := loadPreparedSnapshot(bundle, binding); err != nil {
		return TerminationRecord{}, err
	}
	var record TerminationRecord
	if err := readPrivateJSON(bundle, "termination.json", 1<<20, &record); err != nil {
		return TerminationRecord{}, err
	}
	if record.Version != 1 || record.Binding != binding {
		return TerminationRecord{}, errors.New("invalid retained skill termination binding")
	}
	return record, nil
}

func retainTermination(bundle *os.Root, binding SnapshotBinding, unclean bool) (TerminationRecord, error) {
	if previous, err := ReadTermination(bundle, binding); err == nil {
		return previous, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return TerminationRecord{}, err
	}
	directory, err := privateBundleFile(bundle)
	if err != nil {
		return TerminationRecord{}, err
	}
	defer directory.Close()
	record := TerminationRecord{Version: 1, Binding: binding, Unclean: unclean}
	if err := writePrivateJSON(bundle, directory, "termination.json", record, true); err != nil {
		return TerminationRecord{}, err
	}
	return record, nil
}
