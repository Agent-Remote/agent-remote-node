package skillmanager

import (
	"bytes"
	"encoding/json"
	"errors"
)

// UnmarshalJSON preserves explicit generations and rejects missing, duplicate or aliased bindings.
func (binding *SnapshotBinding) UnmarshalJSON(data []byte) error {
	type plain SnapshotBinding
	var decoded plain
	if err := decodeJournalFields(data, &decoded,
		[]string{"user_id", "account_id", "node_id", "session_id", "snapshot_id", "directory_epoch", "library_generation", "initial_tree_digest"},
		[]string{"task_id", "preparation_digest", "system_releases"}); err != nil {
		return err
	}
	*binding = SnapshotBinding(decoded)
	return nil
}

// UnmarshalJSON requires the original termination classification even when it is false.
func (record *FinalizationRecord) UnmarshalJSON(data []byte) error {
	type plain FinalizationRecord
	var decoded plain
	if err := decodeJournalFields(data, &decoded,
		[]string{"version", "binding", "tree_digest", "unclean", "state"}, []string{"objects_version"}); err != nil {
		return err
	}
	*record = FinalizationRecord(decoded)
	return nil
}

func decodeJournalFields(data []byte, out any, required, optional []string) error {
	if err := validateJSONObject(data, append(append([]string{}, required...), optional...)...); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return errors.New("duplicate journal field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	for _, name := range required {
		if !seen[name] {
			return errors.New("missing journal identity field")
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}
