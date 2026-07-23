package runtime

import "testing"

func TestHostResourcesReportsMemoryAndDisk(t *testing.T) {
	resources := hostResources()
	if resources.MemoryTotalBytes <= 0 || resources.MemoryUsedBytes < 0 {
		t.Fatalf("invalid memory snapshot: %#v", resources)
	}
	if resources.DiskTotalBytes <= 0 || resources.DiskUsedBytes < 0 {
		t.Fatalf("invalid disk snapshot: %#v", resources)
	}
}

func TestStringMapDropsNonStringValues(t *testing.T) {
	result := stringMap(map[string]any{"kernel": "6.8.0", "invalid": true})
	if result["kernel"] != "6.8.0" || len(result) != 1 {
		t.Fatalf("unexpected string map: %#v", result)
	}
}
