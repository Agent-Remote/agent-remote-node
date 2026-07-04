package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const defaultVersion = "0.1.0"

// Config contains local node runtime settings.
type Config struct {
	ServerURL                string   `json:"server_url"`
	NodeID                   string   `json:"node_id"`
	NodeToken                string   `json:"node_token"`
	Version                  string   `json:"version"`
	SupportedToolTypes       []string `json:"supported_tool_types"`
	HeartbeatIntervalSeconds int      `json:"heartbeat_interval_seconds"`
	PollIntervalSeconds      int      `json:"poll_interval_seconds"`
	LedgerPath               string   `json:"ledger_path"`
	SSHAuthorizedKeysPath    string   `json:"ssh_authorized_keys_path"`
	AttachBinaryPath         string   `json:"attach_binary_path"`
	WorkspaceRoot            string   `json:"workspace_root"`
	AccountRoot              string   `json:"account_root"`
	DockerBinaryPath         string   `json:"docker_binary_path"`
	TmuxBinaryPath           string   `json:"tmux_binary_path"`
	MutagenBinaryPath        string   `json:"mutagen_binary_path"`
}

// WithDefaults fills optional config values.
func (c Config) WithDefaults() Config {
	if c.Version == "" {
		c.Version = defaultVersion
	}
	if len(c.SupportedToolTypes) == 0 {
		c.SupportedToolTypes = []string{"claude"}
	}
	if c.HeartbeatIntervalSeconds <= 0 {
		c.HeartbeatIntervalSeconds = 30
	}
	if c.PollIntervalSeconds <= 0 {
		c.PollIntervalSeconds = 5
	}
	if c.LedgerPath == "" {
		c.LedgerPath = "agent-remote-node-ledger.json"
	}
	if c.SSHAuthorizedKeysPath == "" {
		c.SSHAuthorizedKeysPath = "authorized_keys.agent-remote"
	}
	if c.AttachBinaryPath == "" {
		c.AttachBinaryPath = "agent-remote-attach"
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = "/var/lib/agent-remote/users"
	}
	if c.AccountRoot == "" {
		c.AccountRoot = c.WorkspaceRoot
	}
	if c.DockerBinaryPath == "" {
		c.DockerBinaryPath = "docker"
	}
	if c.TmuxBinaryPath == "" {
		c.TmuxBinaryPath = "tmux"
	}
	if c.MutagenBinaryPath == "" {
		c.MutagenBinaryPath = "mutagen"
	}
	return c
}

// Validate checks required config values.
func (c Config) Validate(requireToken bool) error {
	if c.ServerURL == "" {
		return errors.New("server_url is required")
	}
	if c.NodeID == "" {
		return errors.New("node_id is required")
	}
	if requireToken && c.NodeToken == "" {
		return errors.New("node_token is required")
	}
	return nil
}

// Load reads a JSON config file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg = cfg.WithDefaults()
	return cfg, cfg.Validate(false)
}

// Save writes a JSON config file with owner-only permissions.
func Save(path string, cfg Config) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(false); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
