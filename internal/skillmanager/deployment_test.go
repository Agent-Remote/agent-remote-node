package skillmanager

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDeploymentMatchesServerFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/deployment-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var input SkillDeployment
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	if err := input.Validate(input.SkillDeploymentIdentity); err != nil {
		t.Fatal(err)
	}
	if input.Plan.Generation != 9007199254740993 || input.DirectoryEpoch != 9007199254740995 {
		t.Fatal("original integer identity was rounded")
	}
}

func TestDeploymentInputDigestsMatchPythonForUnicodeAndLargeIntegers(t *testing.T) {
	data, err := os.ReadFile("testdata/deployment-input-digests-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name   string          `json:"name"`
		Input  SkillDeployment `json:"input"`
		Digest string          `json:"input_digest"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			digest, err := vector.Input.InputDigest()
			if err != nil || digest != vector.Digest {
				t.Fatal("Python and Go disagree about the complete original input", digest, err)
			}
		})
	}
}

func TestDeploymentJSONRejectsMissingNullAliasedAndNonintegerFields(t *testing.T) {
	data, err := os.ReadFile("testdata/deployment-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"operation_id", "attempt_id", "task_id", "user_id", "node_id", "account_id", "checkpoint_id", "plan_digest", "tree_digest", "runtime_backend", "directory_epoch", "plan", "manifest", "items"} {
		original := fields[field]
		t.Run(field, func(t *testing.T) {
			for _, replacement := range []json.RawMessage{nil, []byte("null")} {
				if replacement == nil {
					delete(fields, field)
				} else {
					fields[field] = replacement
				}
				changed, _ := json.Marshal(fields)
				var decoded SkillDeployment
				if json.Unmarshal(changed, &decoded) == nil {
					t.Fatal("missing or null authority was accepted")
				}
			}
			delete(fields, field)
			fields[strings.ToUpper(field)] = original
			changed, _ := json.Marshal(fields)
			var decoded SkillDeployment
			if json.Unmarshal(changed, &decoded) == nil {
				t.Fatal("case alias was accepted")
			}
			delete(fields, strings.ToUpper(field))
			fields[field] = original
		})
	}
	for _, replacement := range []string{`true`, `"9007199254740993"`, `1.0`, `9223372036854775808`} {
		changed := bytes.Replace(data, []byte(`"generation": 9007199254740993`), []byte(`"generation": `+replacement), 1)
		var decoded SkillDeployment
		if json.Unmarshal(changed, &decoded) == nil {
			t.Fatal("noninteger or overflowing generation was accepted")
		}
	}
	for _, field := range []string{"generation", "enabled", "state_epoch"} {
		needle := []byte(`"` + field + `":`)
		replacement := []byte(`"` + field + `": 1, "` + field + `":`)
		changed := bytes.Replace(data, needle, replacement, 1)
		var decoded SkillDeployment
		if json.Unmarshal(changed, &decoded) == nil {
			t.Fatal("duplicate nested authority was accepted", field)
		}
	}
}

func TestDeploymentPlanDigestSortsWithoutChangingOriginalSourceOrder(t *testing.T) {
	data, err := os.ReadFile("testdata/deployment-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var input SkillDeployment
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	original := input.Plan.Sources[0].SourceID
	first, err := input.Plan.Digest()
	if err != nil || first != input.PlanDigest || input.Plan.Sources[0].SourceID != original {
		t.Fatal("canonical digest mutated original input", err)
	}
	input.Plan.Sources[0], input.Plan.Sources[1] = input.Plan.Sources[1], input.Plan.Sources[0]
	second, err := input.Plan.Digest()
	if err != nil || first != second {
		t.Fatal("source order changed original plan identity", err)
	}
}
