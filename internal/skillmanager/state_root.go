package skillmanager

import (
	"errors"
	"path/filepath"
	"strings"
)

// DefaultStateRoot is independent of transient runtime specs and shared account directories.
const DefaultStateRoot = "/var/lib/agent-remote-skill-state"

// ErrStateStoreAbsent means a no-follow read found an absent directory beneath verified ancestors.
// It does not include linked, inaccessible, malformed or unsafe existing paths.
var ErrStateStoreAbsent = errors.New("skill state store is absent")

// StatePolicy defines explicit runtime-state limits and disk reserves in bytes and percent.
type StatePolicy struct {
	CheckpointBytes  int64  `json:"checkpoint_bytes"`
	DirectoryBytes   int64  `json:"directory_bytes"`
	Entries          int    `json:"entries"`
	MinimumFreeBytes uint64 `json:"minimum_free_bytes"`
	ReservePercent   uint64 `json:"reserve_percent"`
}

// DefaultStatePolicy returns the design's initial per-item, directory and disk reserve limits.
func DefaultStatePolicy() StatePolicy {
	return StatePolicy{CheckpointBytes: 1 << 30, DirectoryBytes: 10 << 30, Entries: 100_000, MinimumFreeBytes: 2 << 30, ReservePercent: 5}
}

// Validate rejects unlimited/empty state quotas and invalid disk reserve percentages.
func (p StatePolicy) Validate() error {
	if p.CheckpointBytes <= 0 || p.DirectoryBytes < p.CheckpointBytes || p.Entries <= 0 || p.Entries > 100_000 || p.MinimumFreeBytes == 0 || p.ReservePercent > 100 {
		return errors.New("invalid skill_state_policy: require positive byte limits, 1–100000 entries and reserve_percent between 0 and 100")
	}
	return nil
}

// ValidateStateRoot rejects overlap with roots cleaned or exposed by other lifecycle components.
// Filesystem ancestry and symlink permissions must additionally be checked by the privileged opener.
func ValidateStateRoot(root string, occupied ...string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) || strings.ContainsAny(root, "\x00\r\n") {
		return errors.New("skill_state_root must be a canonical absolute non-root directory")
	}
	for _, other := range occupied {
		if other == "" {
			continue
		}
		other = filepath.Clean(other)
		if pathsOverlap(root, other) {
			return errors.New("skill_state_root must not overlap runtime, account, workspace, browser or broker roots")
		}
	}
	return nil
}

func pathsOverlap(first, second string) bool {
	if first == string(filepath.Separator) || second == string(filepath.Separator) {
		return true
	}
	return first == second || strings.HasPrefix(first, second+string(filepath.Separator)) || strings.HasPrefix(second, first+string(filepath.Separator))
}
