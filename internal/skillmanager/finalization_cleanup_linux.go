package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
)

type finalizationRuntimeCleanup struct {
	Version     int                `json:"version"`
	Capture     FinalizationRecord `json:"capture"`
	SessionRoot string             `json:"session_root"`
}

func (r *finalizationRuntimeCleanup) UnmarshalJSON(data []byte) error {
	type plain finalizationRuntimeCleanup
	return decodeJournalFields(data, (*plain)(r), []string{"version", "capture", "session_root"}, nil)
}

// FinalizationRuntimeCleaned checks a historical cleanup receipt bound to the original runtime root.
// It does not inspect current runtime liveness or authorize removing any newly appearing resource.
func FinalizationRuntimeCleaned(bundle *os.Root, capture FinalizationRecord, sessionRoot string) (bool, error) {
	journal, err := openFinalizationCleanupJournal(bundle, capture, sessionRoot)
	if err != nil {
		return false, err
	}
	defer journal.Close()
	var receipt finalizationRuntimeCleanup
	if err := readPrivateJSON(journal, "runtime-cleanup.json", 1<<20, &receipt); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if receipt.Version != 1 || receipt.SessionRoot != sessionRoot || receipt.Capture.Validate() != nil || !receipt.Capture.CanDeleteSession() || !SameFinalizationInput(receipt.Capture, capture) || receipt.Capture.State != capture.State {
		return false, errors.New("runtime cleanup receipt differs from original finalization")
	}
	return true, nil
}

// RetainFinalizationRuntimeCleanup saves completion only after the Helper proves stopped resources
// were removed. The caller serializes lifecycle mutations; this never removes retained content.
func RetainFinalizationRuntimeCleanup(bundle *os.Root, capture FinalizationRecord, sessionRoot string) error {
	cleaned, err := FinalizationRuntimeCleaned(bundle, capture, sessionRoot)
	if err != nil {
		return err
	}
	journal, err := openFinalizationCleanupJournal(bundle, capture, sessionRoot)
	if err != nil {
		return err
	}
	defer journal.Close()
	directory, err := privateBundleFile(journal)
	if err != nil {
		return err
	}
	defer directory.Close()
	if cleaned {
		return directory.Sync()
	}
	return writePrivateJSON(journal, directory, "runtime-cleanup.json", finalizationRuntimeCleanup{Version: 1, Capture: capture, SessionRoot: sessionRoot}, true)
}

func openFinalizationCleanupJournal(bundle *os.Root, capture FinalizationRecord, sessionRoot string) (*os.Root, error) {
	if capture.Validate() != nil || !capture.CanDeleteSession() || !filepath.IsAbs(sessionRoot) || filepath.Clean(sessionRoot) != sessionRoot || sessionRoot == "/" {
		return nil, errors.New("runtime cleanup requires complete terminal retention")
	}
	record, err := ReadFinalization(bundle, capture.Binding)
	if err != nil {
		return nil, err
	}
	if !SameFinalizationInput(record, capture) || record.State != capture.State {
		return nil, errors.New("runtime cleanup changed original finalization")
	}
	ack, err := ReadFinalizationAcknowledgement(bundle, capture.Binding)
	if err != nil {
		return nil, err
	}
	state, err := ack.State()
	if err != nil || ack.Publication == nil || state != capture.State {
		return nil, errors.New("runtime cleanup lacks exact publication acknowledgement")
	}
	return bundle.OpenRoot("finalization")
}
