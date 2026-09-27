package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSkillManagerRequiresExplicitBooleanAndSurvivesSave(t *testing.T) {
	for _, raw := range []string{`{}`, `{"skill_manager_enabled":false}`, `{"skill_manager_enabled":true}`} {
		var cfg Config
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		want := raw == `{"skill_manager_enabled":true}`
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
	}
	var cfg Config
	if err := json.Unmarshal([]byte(`{"skill_manager_enabled":"true"}`), &cfg); err == nil {
		t.Fatal("nonboolean intent accepted")
	}
}
