// Package accountmigration defines content-free explicit migration recovery bindings.
package accountmigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Binding pins an independent recovery task to its immutable original migration.
type Binding struct {
	Action               string `json:"action,omitempty"`
	Version              int    `json:"version"`
	TaskID               string `json:"task_id"`
	TaskRecordID         string `json:"task_record_id"`
	OriginalTaskID       string `json:"original_task_id"`
	OriginalTaskRecordID string `json:"original_task_record_id"`
	NodeID               string `json:"node_id"`
	UserID               string `json:"user_id"`
	AccountID            string `json:"tool_account_id"`
	ToolType             string `json:"tool_type"`
	Source               string `json:"source_runtime_backend"`
	Target               string `json:"target_runtime_backend"`
}

// Authorization binds a fresh leased poll attempt to the saved recovery binding.
type Authorization struct {
	Binding      Binding `json:"binding"`
	LeaseAttempt int64   `json:"lease_attempt"`
}

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// Validate rejects ambiguous identifiers and unsupported recovery directions.
func (b Binding) Validate() error {
	for _, id := range []string{b.TaskRecordID, b.OriginalTaskRecordID, b.NodeID, b.UserID, b.AccountID} {
		if !uuidPattern.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return errors.New("invalid migration recovery identity")
		}
	}
	for prefix, id := range map[string]string{"recover_tool_account_runtime:": b.TaskID, "migrate_tool_account_runtime:": b.OriginalTaskID} {
		suffix, ok := strings.CutPrefix(id, prefix+b.AccountID+":")
		if !ok || !uuidPattern.MatchString(suffix) || suffix == "00000000-0000-0000-0000-000000000000" {
			return errors.New("invalid migration recovery task")
		}
	}
	if !((b.Version == 1 && b.Action == "") || (b.Version == 2 && b.Action == "verify_source") || (b.Version == 3 && b.Action == "repair_source")) || b.TaskRecordID == b.OriginalTaskRecordID || b.ToolType != "claude" || b.Source == b.Target || (b.Source != "native" && b.Source != "docker_sandbox") || (b.Target != "native" && b.Target != "docker_sandbox") {
		return errors.New("invalid migration recovery binding")
	}
	return nil
}

// DecodeBinding accepts only the exact versioned task payload.
func DecodeBinding(payload map[string]any) (Binding, error) {
	var binding Binding
	data, err := json.Marshal(payload)
	if err != nil {
		return binding, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return binding, err
	}
	return binding, binding.Validate()
}

// UnmarshalJSON preserves the exact field set of each supported wire version.
func (b *Binding) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	required := []string{"version", "task_id", "task_record_id", "original_task_id", "original_task_record_id", "node_id", "user_id", "tool_account_id", "tool_type", "source_runtime_backend", "target_runtime_backend"}
	for _, key := range required {
		if value, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("missing migration recovery field")
		}
	}
	type wireBinding Binding
	var decoded wireBinding
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	count := len(required)
	if decoded.Version == 2 || decoded.Version == 3 {
		count++
	}
	if len(fields) != count {
		return errors.New("invalid migration recovery fields")
	}
	result := Binding(decoded)
	if err := result.Validate(); err != nil {
		return err
	}
	*b = result
	return nil
}
