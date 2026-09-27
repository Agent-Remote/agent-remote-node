package toolsessions

import (
	"errors"
	"strings"
)

// ErrManagedSkillsUnsupported prevents a managed request from falling back to legacy startup.
var ErrManagedSkillsUnsupported = errors.New("managed skill session preparation is unavailable")

// RequireLegacySkillStartup rejects managed markers before a legacy decoder can discard them.
// Presence, including null or a case alias, requires the dedicated managed preparation path.
func RequireLegacySkillStartup(payload map[string]any) error {
	for key := range payload {
		if strings.EqualFold(key, "skill_manager") {
			return ErrManagedSkillsUnsupported
		}
	}
	return nil
}
