package skilllifecycle

import (
	"strings"
	"testing"
)

const discoveryRun = "ebd1c4f9-6c1f-4a20-a540-60a3c9748008"
const discoveryUse = `{"type":"assistant","sessionId":"ebd1c4f9-6c1f-4a20-a540-60a3c9748008","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"Skill","input":{"skill":"learning"}}]}}` + "\n"
const discoveryResult = `{"type":"user","sessionId":"ebd1c4f9-6c1f-4a20-a540-60a3c9748008","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"Launched skill","is_error":false}]}}` + "\n"

func TestClaudeDiscoveryParser(t *testing.T) {
	valid := discoveryUse + discoveryResult
	cases := []struct {
		name  string
		trace string
		want  bool
	}{
		{"successful_skill", valid, true},
		{"implicit_success", strings.Replace(valid, `,"is_error":false`, "", 1), true},
		{"metadata", "{\"type\":\"file-history-snapshot\"}\n" + valid, true},
		{"empty", "", false},
		{"read_only", strings.Replace(valid, `"name":"Skill"`, `"name":"Read"`, 1), false},
		{"other_skill", strings.Replace(valid, `"skill":"learning"`, `"skill":"other"`, 1), false},
		{"wrong_session", strings.Replace(valid, discoveryRun, "other-session", 1), false},
		{"missing_session", strings.Replace(valid, discoveryRun, "", 1), false},
		{"failed_skill", strings.Replace(valid, `"is_error":false`, `"is_error":true`, 1), false},
		{"null_error", strings.Replace(valid, `"is_error":false`, `"is_error":null`, 1), false},
		{"wrong_result", strings.Replace(discoveryResult, "tool-1", "tool-2", 1) + discoveryUse, false},
		{"result_before_use", discoveryResult + discoveryUse, false},
		{"missing_result", discoveryUse, false},
		{"duplicate_use", discoveryUse + valid, false},
		{"duplicate_result", valid + discoveryResult, false},
		{"wrong_role", strings.Replace(valid, `"role":"assistant"`, `"role":"user"`, 1), false},
		{"malformed_tail", valid + "{private-invalid-json}\n", false},
		{"truncated_tail", strings.TrimSuffix(valid, "\n"), false},
		{"pending_tail", valid + strings.Replace(discoveryUse, "tool-1", "tool-2", 1), false},
		{"oversized_line", valid + strings.Repeat(" ", claudeTraceLineBytes) + "\n", false},
		{"oversized_file", valid + strings.Repeat(" ", claudeTraceBytes) + "\n", false},
		{"too_many_entries", valid + strings.Repeat("{\"type\":\"progress\"}\n", claudeTraceEntries), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyClaudeDiscovery(strings.NewReader(tc.trace), discoveryRun)
			if (err == nil) != tc.want {
				t.Fatal("unexpected discovery validation outcome")
			}
		})
	}
}
