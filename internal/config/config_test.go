package config

import "testing"

func TestWireGuardIPRemovesInterfacePrefix(t *testing.T) {
	cfg := Config{WireGuardAddress: "10.77.0.1/24"}
	if got := cfg.WireGuardIP(); got != "10.77.0.1" {
		t.Fatalf("unexpected WireGuard IP %q", got)
	}
}

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
