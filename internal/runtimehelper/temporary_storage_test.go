package runtimehelper

import (
	"encoding/json"
	"testing"
)

func TestTemporaryStoragePolicyDefaultsAndLimits(t *testing.T) {
	policy, err := parseRuntimePolicy(nil)
	if err != nil || policy.TemporaryStorage != "disk" || policy.TemporarySizeBytes != 16<<30 {
		t.Fatal("new sessions must use bounded disk storage", policy, err)
	}
	for _, size := range []int64{64 << 20, 2 << 30, 16 << 30} {
		policy, err := parseRuntimePolicy(map[string]any{"temporary_size_bytes": float64(size)})
		if err != nil || policy.TemporarySizeBytes != size {
			t.Fatal("valid disk size rejected", size, err)
		}
	}
	for _, value := range []any{float64(0), float64(16<<30) + 1, float64(64<<20) - 1, 1.5, "16G", nil} {
		if _, err := parseRuntimePolicy(map[string]any{"temporary_size_bytes": value}); err == nil {
			t.Fatal("invalid disk size accepted", value)
		}
	}
	for _, storage := range []any{"", "host", true, nil, []any{}, map[string]any{}} {
		if _, err := parseRuntimePolicy(map[string]any{"temporary_storage": storage}); err == nil {
			t.Fatal("invalid storage selector accepted", storage)
		}
	}
}

func TestTemporaryStoragePreservesExplicitAndSavedTmpfsPolicy(t *testing.T) {
	policy, err := parseRuntimePolicy(map[string]any{"temporary_storage": "tmpfs", "tmpfs_size_bytes": float64(512 << 20)})
	if err != nil || policy.TemporaryStorage != "tmpfs" || policy.TmpfsSizeBytes != 512<<20 {
		t.Fatal("explicit tmpfs policy changed", policy, err)
	}
	var saved SessionSpec
	if err := json.Unmarshal([]byte(`{"policy":{"tmpfs_size_bytes":1073741824}}`), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Policy.TemporaryStorage != "" || saved.Policy.TmpfsSizeBytes != 1<<30 {
		t.Fatal("legacy saved spec was silently migrated", saved.Policy)
	}
	policy, err = parseRuntimePolicy(map[string]any{"tmpfs_size_bytes": float64(1 << 30)})
	if err != nil || policy.TemporaryStorage != "disk" || policy.TemporarySizeBytes != 16<<30 {
		t.Fatal("old node policy prevented new-session disk default", policy, err)
	}
}
