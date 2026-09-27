package toolsessions

import (
	"errors"
	"testing"
)

func TestManagedSkillMarkerCannotDisappearDuringLegacyDecoding(t *testing.T) {
	for _, key := range []string{"skill_manager", "Skill_Manager", "SKILL_MANAGER"} {
		for _, value := range []any{nil, false, "", map[string]any{}, map[string]any{
			"protocol_version": 1, "manifest_version": 1,
			"snapshot_id": "11111111-1111-4111-8111-111111111111", "task_id": "22222222-2222-4222-8222-222222222222",
		}} {
			if _, err := DecodeCreatePayload(map[string]any{key: value}); !errors.Is(err, ErrManagedSkillsUnsupported) {
				t.Fatalf("managed marker %q was discarded: %v", key, err)
			}
		}
	}
	if err := RequireLegacySkillStartup(map[string]any{"session_id": "legacy"}); err != nil {
		t.Fatal(err)
	}
}
