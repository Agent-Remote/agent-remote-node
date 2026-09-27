package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillManagerDefaultsEnabledAndPreservesExplicitDisable(t *testing.T) {
	for _, raw := range []string{`{}`, `{"skill_manager_enabled":false}`, `{"skill_manager_enabled":true}`} {
		var cfg Config
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		want := raw != `{"skill_manager_enabled":false}`
		cfg.ServerURL, cfg.NodeID = "https://control.example", "node-test"
		cfg = cfg.WithDefaults()
		path := filepath.Join(t.TempDir(), "config.json")
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil || loaded.SkillManagerEnabled != want {
			t.Fatalf("intent changed: %v", err)
		}
		upgraded, err := LoadForUpgrade(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := SaveForUpgrade(path, upgraded.WithDefaults()); err != nil {
			t.Fatal(err)
		}
		loaded, err = Load(path)
		if err != nil || loaded.SkillManagerEnabled != want {
			t.Fatalf("upgrade changed intent: %v", err)
		}
	}
	for _, raw := range []string{`{"skill_manager_enabled":"true"}`, `{"skill_manager_enabled":null}`} {
		var cfg Config
		if err := json.Unmarshal([]byte(raw), &cfg); err == nil {
			t.Fatal("nonboolean intent accepted")
		}
	}
	if !(Config{}).WithDefaults().SkillManagerEnabled {
		t.Fatal("fresh registration must enable Skill management")
	}
}

func TestSkillManagerFreshInstallExampleEnablesByDefault(t *testing.T) {
	raw, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.SkillManagerEnabled {
		t.Fatal("installer example disables Skill management")
	}
}
