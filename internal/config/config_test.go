package config

import "testing"

func TestValidateRejectsInvalidRuntimeBackends(t *testing.T) {
	for _, backends := range [][]string{
		{},
		{"future"},
		{"native", "native"},
	} {
		cfg := Config{
			ServerURL:              "https://control.example",
			NodeID:                 "node_1",
			AllowedRuntimeBackends: backends,
		}
		if err := cfg.Validate(false); err == nil {
			t.Fatalf("expected backends to be rejected: %#v", backends)
		}
	}
}
