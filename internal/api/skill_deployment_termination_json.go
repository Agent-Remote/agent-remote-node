package api

import (
	"bytes"
	"encoding/json"
	"errors"
)

// UnmarshalJSON preserves strict original poll and bounded failure classification.
func (r *SkillDeploymentTerminationRequest) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentTerminationRequest
	var decoded plain
	if err := decodeDeploymentResultFields(data, &decoded, "lease_attempt", "error_code"); err != nil {
		return err
	}
	*r = SkillDeploymentTerminationRequest(decoded)
	return r.Validate()
}

// UnmarshalJSON requires complete canonical intent and nested original binding fields.
func (i *SkillDeploymentTerminationIntent) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentTerminationIntent
	var decoded plain
	if err := decodeDeploymentResultFields(data, &decoded, "version", "intent_id", "binding", "request", "outcome", "error_code", "retryable"); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := decodeDeploymentResultFields(fields["binding"], &decoded.Binding, "operation_id", "attempt_id", "task_id", "user_id", "account_id", "node_id", "checkpoint_id", "plan_digest", "tree_digest", "runtime_backend"); err != nil {
		return err
	}
	*i = SkillDeploymentTerminationIntent(decoded)
	return i.Validate()
}

// UnmarshalJSON rejects missing or altered authority before any confirmation request.
func (r *SkillDeploymentTerminatedResult) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentTerminatedResult
	var decoded plain
	if err := decodeDeploymentResultFields(data, &decoded, "intent", "drain"); err != nil {
		return err
	}
	*r = SkillDeploymentTerminatedResult(decoded)
	return r.Validate()
}

// UnmarshalJSON requires explicit acceptance and the complete original terminal proposal.
func (o *SkillDeploymentTerminationObservation) UnmarshalJSON(data []byte) error {
	type plain SkillDeploymentTerminationObservation
	var decoded plain
	if err := decodeDeploymentResultFields(data, &decoded, "result", "accepted", "current_lease_attempt", "task_status"); err != nil {
		return err
	}
	*o = SkillDeploymentTerminationObservation(decoded)
	return nil
}

type deploymentTerminationLookup struct {
	Intent  *SkillDeploymentTerminationIntent `json:"intent"`
	present bool
}

func (l *deploymentTerminationLookup) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 1 || fields["intent"] == nil {
		return errors.New("invalid deployment termination lookup")
	}
	type plain deploymentTerminationLookup
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*l = deploymentTerminationLookup(decoded)
	l.present = true
	return nil
}

type terminationEnvelope[T any] skillEnvelope[T]

// UnmarshalJSON prevents missing or aliased envelope data from becoming a false absence observation.
func (e *terminationEnvelope[T]) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"schema_version", "status", "committed", "data", "errors"} {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing deployment termination envelope field")
		}
	}
	for name, value := range fields {
		switch name {
		case "schema_version", "status", "committed", "data", "errors":
		case "operation_id":
			if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errors.New("unexpected deployment termination operation envelope")
			}
		case "retryable":
			if !bytes.Equal(bytes.TrimSpace(value), []byte("false")) {
				return errors.New("unexpected deployment termination retry envelope")
			}
		default:
			return errors.New("unknown deployment termination envelope field")
		}
	}
	var decoded skillEnvelope[T]
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*e = terminationEnvelope[T](decoded)
	return nil
}
