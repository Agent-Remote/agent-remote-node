package skillmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
)

// ListFinalizationSessions selects a bounded sorted page without loading the whole store index.
// It only lists canonical session names; callers inspect each original receipt independently.
func ListFinalizationSessions(ctx context.Context, store *os.Root, cursor string) ([]string, bool, bool, error) {
	if err := ValidateFinalizationCursor(cursor); err != nil {
		return nil, false, false, err
	}
	directory, err := privateBundleFile(store)
	if err != nil {
		return nil, false, false, err
	}
	defer directory.Close()
	names := make([]string, 0, FinalizationPageLimit+2)
	invalid := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, false, err
		}
		entries, err := directory.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, false, err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "session-") {
				continue
			}
			id := strings.TrimPrefix(entry.Name(), "session-")
			if !validSkillUUID(id) {
				invalid = true
				continue
			}
			if id <= cursor {
				continue
			}
			names = append(names, id)
			sort.Strings(names)
			if len(names) > FinalizationPageLimit+1 {
				names = names[:FinalizationPageLimit+1]
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	more := len(names) > FinalizationPageLimit
	if more {
		names = names[:FinalizationPageLimit]
	}
	return names, more, invalid, nil
}
