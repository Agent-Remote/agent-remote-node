package skillmanager

import "errors"

// PermissionBaseline records immutable source modes and the actual writable starting tree.
type PermissionBaseline struct {
	Version      int      `json:"version"`
	Source       Manifest `json:"source"`
	Materialized Manifest `json:"materialized"`
}

// WritableBaseline computes the exact modes that an ordinary writable copy must receive.
func WritableBaseline(source Manifest) (PermissionBaseline, error) {
	if err := Validate(source); err != nil {
		return PermissionBaseline{}, err
	}
	baseline := PermissionBaseline{Version: 1, Source: cloneManifest(source), Materialized: cloneManifest(source)}
	for index := range baseline.Materialized.Entries {
		entry := &baseline.Materialized.Entries[index]
		switch entry.Kind {
		case "file":
			entry.Mode |= 0o600
		case "directory":
			entry.Mode |= 0o700
		}
	}
	return baseline, nil
}

// RestoreSourceModes removes only unchanged preparation-mode adjustments from a final tree.
func RestoreSourceModes(final Manifest, baseline PermissionBaseline) (Manifest, error) {
	if err := Validate(final); err != nil {
		return Manifest{}, err
	}
	expected, err := WritableBaseline(baseline.Source)
	if err != nil {
		return Manifest{}, err
	}
	if baseline.Version != 1 || !equalManifests(expected.Materialized, baseline.Materialized) {
		return Manifest{}, errors.New("invalid materialized permission baseline")
	}
	previous := make(map[string]int, len(baseline.Source.Entries))
	for index, entry := range baseline.Source.Entries {
		previous[entry.Path] = index
	}
	result := cloneManifest(final)
	for index := range result.Entries {
		entry := &result.Entries[index]
		original, exists := previous[entry.Path]
		if !exists {
			continue
		}
		actual := baseline.Materialized.Entries[original]
		if entry.Kind == actual.Kind && entry.Mode == actual.Mode {
			entry.Mode = baseline.Source.Entries[original].Mode
		}
	}
	return result, nil
}

func cloneManifest(value Manifest) Manifest {
	return Manifest{Version: value.Version, Entries: append([]Entry{}, value.Entries...)}
}

func equalManifests(a, b Manifest) bool {
	if a.Version != b.Version || len(a.Entries) != len(b.Entries) {
		return false
	}
	for index := range a.Entries {
		if a.Entries[index] != b.Entries[index] {
			return false
		}
	}
	return true
}
