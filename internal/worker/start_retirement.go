package worker

import (
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

var errManagedStartRetired = errors.New("managed startup proposal was cancelled without acceptance")

// Cancellation fences all later confirmations under the Server's task lock. Lease expiry or
// same-attempt absence alone cannot prove that an in-flight proposal will remain unaccepted.
func cancelledManagedStart(outcome api.ManagedSessionStartResult, observed api.ManagedStartObservation) bool {
	return observed.Result == outcome && !observed.Accepted && observed.TaskStatus == "cancelled" &&
		observed.CurrentLeaseAttempt >= outcome.LeaseAttempt && observed.CurrentLeaseAttempt <= 2147483647
}

func (j managedStartJournal) retire(previous managedStartEntry, observed api.ManagedStartObservation) error {
	if !cancelledManagedStart(previous.record.Outcome, observed) {
		return ledger.ErrConflict
	}
	if previous.entry.Status == managedStartRetired && previous.retirement == observed {
		return nil
	}
	if previous.entry.Status != managedStartPending {
		return ledger.ErrConflict
	}
	data, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	var evidence map[string]any
	if err := json.Unmarshal(data, &evidence); err != nil {
		return err
	}
	retired := previous.entry
	retired.Status = managedStartRetired
	retired.Result = make(map[string]any, len(previous.entry.Result)+1)
	for key, value := range previous.entry.Result {
		retired.Result[key] = value
	}
	retired.Result["retirement"] = evidence
	err = j.ledger.CompareAndSwap(&previous.entry, retired)
	if errors.Is(err, ledger.ErrConflict) {
		current, loadErr := j.load(previous.entry.TaskID)
		if loadErr != nil {
			return loadErr
		}
		if current != nil && current.record == previous.record && current.entry.Status == managedStartRetired && current.retirement == observed {
			return nil
		}
	}
	return err
}
