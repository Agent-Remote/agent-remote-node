package config

import (
	"encoding/json"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestLegacyConfigGetsIndependentSkillStateDefaults(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"server_url":"https://control.example","node_id":"node"}`), &config); err != nil {
		t.Fatal(err)
	}
	config = config.WithDefaults()
	if config.SkillStateRoot != skillmanager.DefaultStateRoot || config.SkillStatePolicy == nil || *config.SkillStatePolicy != skillmanager.DefaultStatePolicy() {
		t.Fatal("legacy config lacks explicit state defaults")
	}
	if err := config.Validate(false); err != nil {
		t.Fatal(err)
	}
	config.SkillStateRoot = config.AccountRoot + "/skills"
	if err := config.Validate(false); err == nil {
		t.Fatal("private skill state overlaps a shared account tree")
	}
}

func TestExplicitInvalidSkillPolicyIsNotSilentlyDefaulted(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"server_url":"https://control.example","node_id":"node","skill_state_policy":{"directory_bytes":0}}`), &config); err != nil {
		t.Fatal(err)
	}
	if err := config.WithDefaults().Validate(false); err == nil {
		t.Fatal("invalid explicit policy silently became unlimited or defaulted")
	}
}
