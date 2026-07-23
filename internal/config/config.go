package config

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/wireguard"
)

// DefaultVersion is overridden by release builds through Go ldflags.
var DefaultVersion = "0.0.4-fix.7"

// Config contains local node runtime settings.
type Config struct {
	SourcePath               string   `json:"-"`
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
	BrowserRoot              string   `json:"browser_root"`
	BrowserImage             string   `json:"browser_image"`
	BrowserPublicBaseURL     string   `json:"browser_public_base_url"`
	AllowedRuntimeBackends   []string `json:"allowed_runtime_backends"`
	RuntimeSocketPath        string   `json:"runtime_socket_path"`
	RuntimeBinaryPath        string   `json:"runtime_binary_path"`
	ClaudeRuntimePath        string   `json:"claude_runtime_path"`
	WireGuardInterface       string   `json:"wireguard_interface"`
	WireGuardPrivateKeyPath  string   `json:"wireguard_private_key_path"`
	WireGuardAddress         string   `json:"wireguard_address"`
	WireGuardPublicKey       string   `json:"wireguard_public_key"`
	WireGuardEndpoint        string   `json:"wireguard_endpoint"`
	WireGuardListenPort      int      `json:"wireguard_listen_port"`
}

// WithDefaults fills optional config values.
func (c Config) WithDefaults() Config {
	if c.Version == "" {
		c.Version = DefaultVersion
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
	if c.BrowserRoot == "" {
		c.BrowserRoot = "/var/lib/agent-remote/browser-sessions"
	}
	if c.BrowserImage == "" {
		c.BrowserImage = "kasmweb/chrome:1.18.0"
	}
	if len(c.AllowedRuntimeBackends) == 0 {
		c.AllowedRuntimeBackends = []string{"docker_sandbox"}
	}
	if c.RuntimeSocketPath == "" {
		c.RuntimeSocketPath = "/run/agent-remote/runtime.sock"
	}
	if c.RuntimeBinaryPath == "" {
		c.RuntimeBinaryPath = "/usr/local/bin/agent-remote-runtime"
	}
	if c.ClaudeRuntimePath == "" {
		c.ClaudeRuntimePath = "/opt/agent-remote/runtimes/claude/current/bin/claude"
	}
	if c.WireGuardInterface == "" {
		c.WireGuardInterface = "agent-remote"
	}
	if c.WireGuardPrivateKeyPath == "" {
		c.WireGuardPrivateKeyPath = "/etc/agent-remote-node/wireguard.key"
	}
	if c.WireGuardListenPort <= 0 {
		c.WireGuardListenPort = 51820
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
	if len(c.AllowedRuntimeBackends) == 0 {
		return errors.New("allowed_runtime_backends requires at least one backend")
	}
	if err := wireguard.ValidateInterface(c.WireGuardInterface); err != nil {
		return err
	}
	if c.WireGuardListenPort < 1 || c.WireGuardListenPort > 65535 {
		return errors.New("wireguard_listen_port is invalid")
	}
	wireGuardConfigured := c.WireGuardPublicKey != "" || c.WireGuardAddress != "" || c.WireGuardEndpoint != ""
	if wireGuardConfigured {
		if err := wireguard.ValidateKey(c.WireGuardPublicKey); err != nil {
			return errors.New("wireguard_public_key is invalid")
		}
		address, err := netip.ParsePrefix(c.WireGuardAddress)
		if err != nil || !address.Addr().Is4() || address.Bits() < 8 || address.Bits() > 32 {
			return errors.New("wireguard_address must be an IPv4 CIDR")
		}
		host, port, err := net.SplitHostPort(c.WireGuardEndpoint)
		if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" || strings.ContainsAny(host, " \t\r\n") {
			return errors.New("wireguard_endpoint must contain a host and port")
		}
	}
	seenBackends := make(map[string]bool, len(c.AllowedRuntimeBackends))
	for _, backend := range c.AllowedRuntimeBackends {
		if backend != "docker_sandbox" && backend != "native" {
			return errors.New("allowed_runtime_backends contains an unsupported backend")
		}
		if seenBackends[backend] {
			return errors.New("allowed_runtime_backends contains a duplicate backend")
		}
		seenBackends[backend] = true
	}
	return nil
}

// WireGuardIP returns the host address without the local interface prefix.
func (c Config) WireGuardIP() string {
	prefix, err := netip.ParsePrefix(c.WireGuardAddress)
	if err != nil {
		return ""
	}
	return prefix.Addr().String()
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
	cfg.SourcePath = path
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
