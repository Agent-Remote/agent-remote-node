package accountmigration

import (
	"encoding/json"
	"testing"
)

func TestRecoveryBindingVersionedActions(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	original := Binding{Version: 1, TaskID: "recover_tool_account_runtime:" + id + ":" + id,
		OriginalTaskID: "migrate_tool_account_runtime:" + id + ":" + id,
		TaskRecordID:   id, OriginalTaskRecordID: "22222222-2222-4222-8222-222222222222",
		NodeID: id, UserID: id, AccountID: id, ToolType: "claude", Source: "native", Target: "docker_sandbox"}
	for _, version := range []int{1, 2, 3} {
		binding := original
		binding.Version = version
		if version == 2 {
			binding.Action = "verify_source"
		}
		if version == 3 {
			binding.Action = "repair_source"
		}
		wire, err := json.Marshal(binding)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		wantFields := 12
		if version == 1 {
			wantFields = 11
		}
		if len(fields) != wantFields {
			t.Fatal("wire field count changed")
		}
		decoded, err := DecodeBinding(fields)
		if err != nil || decoded != binding {
			t.Fatal("valid binding rejected", err)
		}
		wrongAction := "repair_source"
		if version == 3 {
			wrongAction = "verify_source"
		}
		for _, bad := range []any{nil, "", "repair", 1, wrongAction} {
			fields["action"] = bad
			if _, err := DecodeBinding(fields); err == nil {
				t.Fatal("invalid action accepted", version, bad)
			}
		}
		if version == 1 {
			fields["action"] = "verify_source"
			if _, err := DecodeBinding(fields); err == nil {
				t.Fatal("v1 action accepted")
			}
		} else {
			delete(fields, "action")
			if _, err := DecodeBinding(fields); err == nil {
				t.Fatal("v2 without action accepted")
			}
		}
	}
}
