package config

import (
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
)

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

func TestEgoBrowserDefaultsPinOfficialSkill(t *testing.T) {
	cfg := (Config{ServerURL: "https://control.example", NodeID: "node_1"}).WithDefaults()
	if cfg.EgoBrowserSkillVersion != egobrowserartifact.OfficialSkillVersion ||
		cfg.EgoBrowserSkillTreeSHA256 != egobrowserartifact.OfficialSkillTreeSHA256 ||
		cfg.EgoBrowserSkillPath != "/opt/agent-remote/ego-browser/current/skill/ego-browser" {
		t.Fatalf("unexpected official Skill defaults: %#v", cfg)
	}
}

func TestValidateRejectsUnpinnedEgoBrowserSkill(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(cfg *Config) { cfg.EgoBrowserSkillVersion = "latest" },
		func(cfg *Config) {
			cfg.EgoBrowserSkillTreeSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		func(cfg *Config) { cfg.EgoBrowserSkillPath = "relative/skill" },
	} {
		cfg := (Config{ServerURL: "https://control.example", NodeID: "node_1"}).WithDefaults()
		cfg.EgoBrowserEnabled = true
		mutate(&cfg)
		if err := cfg.Validate(false); err == nil {
			t.Fatalf("unpinned ego-browser Skill was accepted: %#v", cfg)
		}
	}
}
