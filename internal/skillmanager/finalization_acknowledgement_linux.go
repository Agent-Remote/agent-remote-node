package skillmanager

import (
	"context"
	"errors"
	"os"
	"reflect"
)

// ReadFinalizationAcknowledgement reads the retained exact Server receipts without advancing state.
func ReadFinalizationAcknowledgement(bundle *os.Root, binding SnapshotBinding) (FinalizationAcknowledgement, error) {
	record, err := ReadFinalization(bundle, binding)
	if err != nil {
		return FinalizationAcknowledgement{}, err
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return FinalizationAcknowledgement{}, err
	}
	defer journal.Close()
	return readFinalizationAcknowledgement(journal, record)
}

func readFinalizationAcknowledgement(journal *os.Root, record FinalizationRecord) (FinalizationAcknowledgement, error) {
	var ack FinalizationAcknowledgement
	if err := readPrivateJSON(journal, "acknowledgement.json", 1<<20, &ack); err != nil {
		return ack, err
	}
	if err := ack.Validate(); err != nil {
		return ack, err
	}
	if !SameFinalizationInput(record, ack.Capture) {
		return ack, errors.New("retained acknowledgement belongs to different input")
	}
	return ack, nil
}

// AcknowledgeFinalization durably retains exact Server receipts before advancing the local journal.
// The caller authorizes the worker peer and serializes this with lifecycle mutations. A crash after
// acknowledgement publication is resumed by the same operation; there is no generic state grant.
func AcknowledgeFinalization(ctx context.Context, bundle *os.Root, ack FinalizationAcknowledgement) (FinalizationRecord, error) {
	target, err := ack.State()
	if err != nil {
		return FinalizationRecord{}, err
	}
	record, err := ReadFinalization(bundle, ack.Capture.Binding)
	if err != nil {
		return FinalizationRecord{}, err
	}
	if !SameFinalizationInput(record, ack.Capture) {
		return FinalizationRecord{}, errors.New("acknowledgement changed frozen capture")
	}
	path, err := finalizationAcknowledgementPath(record, target)
	if err != nil {
		return FinalizationRecord{}, err
	}
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer journal.Close()
	previous, err := readFinalizationAcknowledgement(journal, record)
	absent := errors.Is(err, os.ErrNotExist)
	if err != nil && !absent {
		return FinalizationRecord{}, err
	}
	if !absent {
		if err := ack.Follows(previous); err != nil {
			return FinalizationRecord{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return FinalizationRecord{}, err
	}
	directory, err := privateBundleFile(journal)
	if err != nil {
		return FinalizationRecord{}, err
	}
	defer directory.Close()
	if !absent && reflect.DeepEqual(previous, ack) {
		// Re-sync a replay in case a prior rename succeeded but its directory sync was uncertain.
		if err := directory.Sync(); err != nil {
			return FinalizationRecord{}, err
		}
	} else {
		if err := writePrivateJSON(journal, directory, "acknowledgement.json", ack, absent); err != nil {
			return FinalizationRecord{}, err
		}
	}
	for _, next := range path {
		if err := ctx.Err(); err != nil {
			return FinalizationRecord{}, err
		}
		record, err = AdvanceFinalization(bundle, ack.Capture.Binding, record.State, next)
		if err != nil {
			return FinalizationRecord{}, err
		}
	}
	return record, nil
}

func finalizationAcknowledgementPath(record FinalizationRecord, target string) ([]string, error) {
	state := record.State
	var path []string
	for state != target {
		next := ""
		switch state {
		case "local_durable":
			next = "upload_pending"
		case "upload_pending":
			next = "persisted"
			if record.Unclean {
				next = "persisted_unclean"
			}
		case "persisted", "persisted_unclean":
			next = target
		default:
			return nil, errors.New("finalization acknowledgement cannot replace terminal state")
		}
		if !allowedFinalizationTransition(state, next, record.Unclean) {
			return nil, errors.New("finalization acknowledgement regressed local state")
		}
		path = append(path, next)
		state = next
	}
	return path, nil
}
