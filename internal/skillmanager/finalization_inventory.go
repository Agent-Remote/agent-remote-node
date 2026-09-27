package skillmanager

import "errors"

// FinalizationPageLimit bounds retained-session inspection and response size per request.
const FinalizationPageLimit = 16

// FinalizationInventoryItem reports one canonical session without exposing private paths.
// A nil record always has an explicit diagnostic; it never certifies successful finalization.
type FinalizationInventoryItem struct {
	SessionID string              `json:"session_id"`
	Record    *FinalizationRecord `json:"record"`
	Code      string              `json:"code"`
}

// FinalizationPage is a sorted, non-snapshot inventory of retained session bundles.
// New entries behind the cursor are discovered on the next complete pass.
type FinalizationPage struct {
	Items        []FinalizationInventoryItem `json:"items"`
	NextCursor   string                      `json:"next_cursor"`
	InvalidNames bool                        `json:"invalid_names"`
}

// ValidateFinalizationCursor rejects paths and noncanonical identities before store access.
func ValidateFinalizationCursor(cursor string) error {
	if cursor != "" && !validSkillUUID(cursor) {
		return errors.New("invalid finalization inventory cursor")
	}
	return nil
}

// Validate binds a page to the original cursor and node, including every diagnostic entry.
func (page FinalizationPage) Validate(cursor, nodeID string) error {
	if err := ValidateFinalizationCursor(cursor); err != nil {
		return err
	}
	if !validSkillUUID(nodeID) || page.Items == nil || len(page.Items) > FinalizationPageLimit {
		return errors.New("invalid finalization inventory page")
	}
	previous := cursor
	for _, item := range page.Items {
		if !validSkillUUID(item.SessionID) || item.SessionID <= previous {
			return errors.New("unordered finalization inventory")
		}
		previous = item.SessionID
		if item.Record != nil {
			if item.Code != "" || item.Record.Validate() != nil || item.Record.Binding.SessionID != item.SessionID || item.Record.Binding.NodeID != nodeID {
				return errors.New("finalization inventory changed original binding")
			}
		} else {
			switch item.Code {
			case "not_finalized", "invalid_retained_state", "unsupported_backend", "reclamation_pending", "content_reclaimed":
			default:
				return errors.New("invalid finalization inventory diagnostic")
			}
		}
	}
	if page.NextCursor != "" && (len(page.Items) != FinalizationPageLimit || page.NextCursor != previous) {
		return errors.New("invalid finalization inventory continuation")
	}
	return nil
}
