package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const finalizationTransferStatus = "skill_finalization_transfer"

type finalizationTransferRecord struct {
	SchemaVersion int                             `json:"schema_version"`
	Capture       skillmanager.FinalizationRecord `json:"capture"`
	Receipt       *api.SkillFinalization          `json:"receipt"`
	Publication   *api.SkillPublication           `json:"publication"`
}

type finalizationTransferEntry struct {
	entry  ledger.Entry
	record finalizationTransferRecord
}

// This ledger is separate from task execution: snapshots outlive both task delivery and sessions.
type finalizationTransferJournal struct{ ledger *ledger.Ledger }

func finalizationInput(capture skillmanager.FinalizationRecord) api.SkillFinalizationInput {
	return api.SkillFinalizationInput{SnapshotID: capture.Binding.SnapshotID, SessionID: capture.Binding.SessionID,
		IdempotencyKey: "skill-finalization:" + capture.Binding.SnapshotID, TreeDigest: capture.TreeDigest, Unclean: capture.Unclean}
}

func sameFinalizationCapture(first, second skillmanager.FinalizationRecord) bool {
	return skillmanager.SameFinalizationInput(first, second)
}

func (r finalizationTransferRecord) validate() error {
	if r.SchemaVersion != 1 || r.Capture.ObjectsVersion != 1 || r.Capture.Validate() != nil {
		return errors.New("invalid finalization transfer journal")
	}
	input := finalizationInput(r.Capture)
	if r.Receipt != nil {
		if err := r.Receipt.ValidateReceipt(input, nil); err != nil {
			return err
		}
	}
	if r.Publication != nil {
		if r.Receipt == nil {
			return errors.New("publication lacks retained finalization")
		}
		return r.Publication.ValidateReceipt(input, *r.Receipt)
	}
	return nil
}

func (j finalizationTransferJournal) load(capture skillmanager.FinalizationRecord) (*finalizationTransferEntry, error) {
	entry, exists, err := j.ledger.Get(capture.Binding.SnapshotID)
	if err != nil || !exists {
		return nil, err
	}
	if entry.TaskID != capture.Binding.SnapshotID || entry.Status != finalizationTransferStatus || entry.Error != nil {
		return nil, errors.New("invalid finalization transfer entry")
	}
	data, err := json.Marshal(entry.Result)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if !exactJournalFields(fields, "schema_version", "capture", "receipt", "publication") {
		return nil, errors.New("invalid finalization transfer fields")
	}
	var record finalizationTransferRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if err := record.validate(); err != nil {
		return nil, err
	}
	if !sameFinalizationCapture(record.Capture, capture) {
		return nil, errors.New("finalization transfer changed its original capture")
	}
	return &finalizationTransferEntry{entry: entry, record: record}, nil
}

func (j finalizationTransferJournal) save(previous *finalizationTransferEntry, record finalizationTransferRecord) (*finalizationTransferEntry, error) {
	if err := record.validate(); err != nil {
		return nil, err
	}
	var expected *ledger.Entry
	if previous != nil {
		if !sameFinalizationCapture(previous.record.Capture, record.Capture) {
			return nil, ledger.ErrConflict
		}
		if previous.record.Receipt != nil {
			if record.Receipt == nil {
				return nil, ledger.ErrConflict
			}
			if err := record.Receipt.ValidateReceipt(finalizationInput(record.Capture), previous.record.Receipt); err != nil {
				return nil, err
			}
		}
		if previous.record.Publication != nil && !reflect.DeepEqual(previous.record.Publication, record.Publication) {
			return nil, ledger.ErrConflict
		}
		expected = &previous.entry
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	// Preserve signed 64-bit generations and upload attempts through the untyped ledger boundary.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	entry := ledger.Entry{TaskID: record.Capture.Binding.SnapshotID, Status: finalizationTransferStatus, Result: result}
	if err := j.ledger.CompareAndSwap(expected, entry); err != nil {
		return nil, err
	}
	return j.load(record.Capture)
}
