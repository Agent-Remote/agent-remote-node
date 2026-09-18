package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
)

func TestLoadMigratesMissingEgoBrowserEnabledToFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"server_url":"https://control.example","node_id":"node_1","allowed_runtime_backends":["docker_sandbox"]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EgoBrowserEnabled || cfg.EgoBrowserConfiguredEnabled() {
		t.Fatalf("missing ego-browser intent was not fail-closed: %#v", cfg)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if value, ok := fields["ego_browser_enabled"].(bool); !ok || value {
		t.Fatalf("migrated config did not persist explicit false: %#v", fields)
	}
}

func TestLoadPreservesExplicitEgoBrowserIntent(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "config.json")
		cfg := (Config{ServerURL: "https://control.example", NodeID: "node_1", EgoBrowserEnabled: enabled}).WithDefaults()
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.EgoBrowserEnabled != enabled {
			t.Fatalf("explicit intent changed: want %t got %#v", enabled, loaded)
		}
	}
}

func TestLoadRejectsMalformedEgoBrowserIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"server_url":"https://control.example","node_id":"node_1","ego_browser_enabled":"true"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("non-boolean ego-browser intent was accepted")
	}
}

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
	if cfg.EgoBrowserWrapperVersion != egobrowserartifact.PinnedWrapperVersion ||
		cfg.EgoBrowserSkillVersion != egobrowserartifact.OfficialSkillVersion ||
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

func TestValidateRejectsStaleEnabledEgoBrowserWrapper(t *testing.T) {
	cfg := (Config{
		ServerURL:                "https://control.example",
		NodeID:                   "node_1",
		EgoBrowserEnabled:        true,
		EgoBrowserWrapperVersion: "0.1.0",
	}).WithDefaults()
	if err := cfg.Validate(false); err == nil {
		t.Fatal("stale enabled ego-browser wrapper pin was accepted")
	}
	if err := cfg.ValidateForUpgrade(false); err != nil {
		t.Fatalf("upgrade validation rejected stale wrapper pin: %v", err)
	}
	cfg.EgoBrowserWrapperVersion = "../untrusted"
	if err := cfg.ValidateForUpgrade(false); err == nil {
		t.Fatal("malformed stale ego-browser wrapper pin was accepted")
	}
}

func TestSaveRejectsConfigSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	configPath := filepath.Join(directory, "config.json")
	original := []byte("do not replace\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ServerURL: "https://control.example", NodeID: "node_1"}
	if err := Save(configPath, cfg); err == nil {
		t.Fatal("config symlink was accepted")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("config symlink target was changed: %q", got)
	}
}

func TestSaveWritesOwnerOnlyConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg := Config{ServerURL: "https://control.example", NodeID: "node_1"}
	if err := Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}
