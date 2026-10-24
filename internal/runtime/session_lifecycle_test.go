package runtime

import (
	"encoding/json"
	"testing"
)

func TestSessionLifecycleCapabilityRequiresExactHealthyHelperReport(t *testing.T) {
	for _, version := range []any{json.Number("1"), json.Number("1.0"), float64(1), "1", true, nil} {
		report := map[string]any{"native": map[string]any{"supported": true, "protocol_version": version}, "unknown": map[string]any{"supported": true, "protocol_version": json.Number("1")}}
		got := sessionLifecycleCapabilities([]string{"native", "unknown"}, report)
		if (len(got) == 1) != (version == json.Number("1")) {
			t.Fatalf("accepted malformed version %v: %v", version, got)
		}
		if len(sessionLifecycleCapabilities(nil, report)) != 0 {
			t.Fatal("disabled backend advertised lifecycle")
		}
	}
	for _, entry := range []map[string]any{{"supported": true}, {"supported": true, "protocol_version": json.Number("1"), "extra": true}, {"supported": "true", "protocol_version": json.Number("1")}} {
		if len(sessionLifecycleCapabilities([]string{"native"}, map[string]any{"native": entry})) != 0 {
			t.Fatal("malformed entry retained support")
		}
	}
}
