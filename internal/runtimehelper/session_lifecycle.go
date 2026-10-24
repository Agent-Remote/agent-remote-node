package runtimehelper

import (
	"bytes"
	"encoding/json"
	"errors"
)

var errSessionClosed = errors.New("SESSION_ADMISSION_CLOSED")
var errSessionStopPending = errors.New("SESSION_STOP_PENDING")

const recordSessionStopOperation = "record_session_stop_intent"

type sessionStopIntent struct {
	Version   int    `json:"version"`
	NodeID    string `json:"node_id"`
	SessionID string `json:"session_id"`
	Scope     string `json:"scope"`
	RequestID string `json:"first_request_id"`
	Operation string `json:"first_operation"`
	Backend   string `json:"requested_backend"`
}

// SessionExecution describes current private Helper admission, separately from an immutable result.
// It is never forwarded to the control plane or used as proof of process termination.
type SessionExecution struct {
	Version        int    `json:"protocol_version"`
	SessionID      string `json:"session_id"`
	Backend        string `json:"runtime_backend"`
	ReceiptReplay  bool   `json:"receipt_replay"`
	FreshAdmission bool   `json:"fresh_admission"`
	StopIntent     bool   `json:"stop_intent"`
}

// UnmarshalJSON requires every exact field; omitted/null booleans cannot imply admission.
func (observation *SessionExecution) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 6 || !bytes.Equal(bytes.TrimSpace(fields["protocol_version"]), []byte("1")) {
		return errors.New("invalid session execution version or fields")
	}
	var value SessionExecution
	value.Version = 1
	for name, target := range map[string]*string{"session_id": &value.SessionID, "runtime_backend": &value.Backend} {
		raw := bytes.TrimSpace(fields[name])
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, target) != nil {
			return errors.New("invalid session execution identity")
		}
	}
	for name, target := range map[string]*bool{"receipt_replay": &value.ReceiptReplay, "fresh_admission": &value.FreshAdmission, "stop_intent": &value.StopIntent} {
		raw := bytes.TrimSpace(fields[name])
		if bytes.Equal(raw, []byte("true")) {
			*target = true
		} else if !bytes.Equal(raw, []byte("false")) {
			return errors.New("invalid session execution flag")
		}
	}
	if validateID(value.SessionID, "session_id") != nil || value.Backend != "native" && value.Backend != "docker_sandbox" || value.FreshAdmission && (value.ReceiptReplay || value.StopIntent) {
		return errors.New("inconsistent session execution")
	}
	*observation = value
	return nil
}

func lifecycleName(session, suffix string) string {
	return shortDigest("tool_session\x00"+session, 64) + suffix
}

func (intent sessionStopIntent) validate(node, session string) error {
	if intent.Version != 1 || intent.NodeID != node || intent.SessionID != session || intent.Scope != "tool_session" || (intent.Backend != "native" && intent.Backend != "docker_sandbox") {
		return errors.New("invalid session stop identity")
	}
	for _, id := range []string{node, session, intent.RequestID} {
		if err := validateID(id, "lifecycle identity"); err != nil {
			return err
		}
	}
	switch intent.Operation {
	case "stop_session", "docker_stop_session", "cleanup_resources", managedCancelOperation, recordSessionStopOperation:
		return nil
	default:
		return errors.New("invalid session stop operation")
	}
}
