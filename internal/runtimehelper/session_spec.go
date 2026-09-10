package runtimehelper

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const sessionRuntimeConfigVersion = 1

// SessionRuntimeConfig is the non-sensitive host configuration snapshot
// written into a root-owned session specification.
//
// It deliberately excludes node credentials and all process-only capability
// material. The snapshot lets an unprivileged session validate its own spec
// without opening the protected node configuration file.
type SessionRuntimeConfig struct {
	Version                   int    `json:"version"`
	StateRoot                 string `json:"state_root"`
	WorkspaceRoot             string `json:"workspace_root"`
	AccountRoot               string `json:"account_root"`
	RuntimeBinaryPath         string `json:"runtime_binary_path"`
	ClaudeRuntimePath         string `json:"claude_runtime_path"`
	DeviceProxyPath           string `json:"device_proxy_path"`
	TmuxBinaryPath            string `json:"tmux_binary_path"`
	BubblewrapPath            string `json:"bubblewrap_path"`
	EgoBrowserEnabled         bool   `json:"ego_browser_enabled"`
	EgoBrowserWrapperPath     string `json:"ego_browser_wrapper_path"`
	EgoBrowserBrokerSocket    string `json:"ego_browser_broker_socket"`
	EgoBrowserBrokerRoot      string `json:"ego_browser_broker_root"`
	EgoBrowserProtocolVersion string `json:"ego_browser_protocol_version"`
	EgoBrowserWrapperVersion  string `json:"ego_browser_wrapper_version"`
	EgoBrowserSkillPath       string `json:"ego_browser_skill_path"`
	EgoBrowserSkillVersion    string `json:"ego_browser_skill_version"`
	EgoBrowserSkillTreeSHA256 string `json:"ego_browser_skill_tree_sha256"`
}

func sessionRuntimeConfigFromEngine(config EngineConfig) *SessionRuntimeConfig {
	config = config.WithDefaults()
	return &SessionRuntimeConfig{
		Version:                   sessionRuntimeConfigVersion,
		StateRoot:                 filepath.Clean(config.StateRoot),
		WorkspaceRoot:             filepath.Clean(config.WorkspaceRoot),
		AccountRoot:               filepath.Clean(config.AccountRoot),
		RuntimeBinaryPath:         filepath.Clean(config.RuntimeBinaryPath),
		ClaudeRuntimePath:         filepath.Clean(config.ClaudeRuntimePath),
		DeviceProxyPath:           filepath.Clean(config.DeviceProxyPath),
		TmuxBinaryPath:            filepath.Clean(config.TmuxBinaryPath),
		BubblewrapPath:            filepath.Clean(config.BubblewrapPath),
		EgoBrowserEnabled:         config.EgoBrowserEnabled,
		EgoBrowserWrapperPath:     filepath.Clean(config.EgoBrowserWrapperPath),
		EgoBrowserBrokerSocket:    filepath.Clean(config.EgoBrowserBrokerSocket),
		EgoBrowserBrokerRoot:      filepath.Clean(config.EgoBrowserBrokerRoot),
		EgoBrowserProtocolVersion: config.EgoBrowserProtocolVersion,
		EgoBrowserWrapperVersion:  config.EgoBrowserWrapperVersion,
		EgoBrowserSkillPath:       filepath.Clean(config.EgoBrowserSkillPath),
		EgoBrowserSkillVersion:    config.EgoBrowserSkillVersion,
		EgoBrowserSkillTreeSHA256: config.EgoBrowserSkillTreeSHA256,
	}
}

func (snapshot SessionRuntimeConfig) validate() error {
	if snapshot.Version != sessionRuntimeConfigVersion {
		return errors.New("session runtime configuration version is unsupported")
	}
	for name, value := range map[string]string{
		"state_root":                snapshot.StateRoot,
		"workspace_root":            snapshot.WorkspaceRoot,
		"account_root":              snapshot.AccountRoot,
		"claude_runtime_path":       snapshot.ClaudeRuntimePath,
		"device_proxy_path":         snapshot.DeviceProxyPath,
		"ego_browser_wrapper_path":  snapshot.EgoBrowserWrapperPath,
		"ego_browser_broker_socket": snapshot.EgoBrowserBrokerSocket,
		"ego_browser_broker_root":   snapshot.EgoBrowserBrokerRoot,
		"ego_browser_skill_path":    snapshot.EgoBrowserSkillPath,
	} {
		if !safeManagedPath(value) {
			return fmt.Errorf("session runtime configuration %s is invalid", name)
		}
	}
	for name, value := range map[string]string{
		"runtime_binary_path": snapshot.RuntimeBinaryPath,
		"tmux_binary_path":    snapshot.TmuxBinaryPath,
		"bubblewrap_path":     snapshot.BubblewrapPath,
	} {
		if !validCommandReference(value) {
			return fmt.Errorf("session runtime configuration %s is invalid", name)
		}
	}
	if snapshot.EgoBrowserProtocolVersion == "" || snapshot.EgoBrowserWrapperVersion == "" ||
		snapshot.EgoBrowserSkillVersion == "" || !validOpaqueContext(snapshot.EgoBrowserProtocolVersion, 128) ||
		!validOpaqueContext(snapshot.EgoBrowserWrapperVersion, 128) ||
		!validOpaqueContext(snapshot.EgoBrowserSkillVersion, 128) {
		return errors.New("session runtime configuration contains invalid ego-browser versions")
	}
	if !validLowerHexDigest(snapshot.EgoBrowserSkillTreeSHA256) {
		return errors.New("session runtime configuration contains an invalid ego-browser digest")
	}
	return nil
}

func (snapshot SessionRuntimeConfig) apply(base EngineConfig) EngineConfig {
	base = base.WithDefaults()
	base.StateRoot = snapshot.StateRoot
	base.WorkspaceRoot = snapshot.WorkspaceRoot
	base.AccountRoot = snapshot.AccountRoot
	base.RuntimeBinaryPath = snapshot.RuntimeBinaryPath
	base.ClaudeRuntimePath = snapshot.ClaudeRuntimePath
	base.DeviceProxyPath = snapshot.DeviceProxyPath
	base.TmuxBinaryPath = snapshot.TmuxBinaryPath
	base.BubblewrapPath = snapshot.BubblewrapPath
	base.EgoBrowserEnabled = snapshot.EgoBrowserEnabled
	base.EgoBrowserWrapperPath = snapshot.EgoBrowserWrapperPath
	base.EgoBrowserBrokerSocket = snapshot.EgoBrowserBrokerSocket
	base.EgoBrowserBrokerRoot = snapshot.EgoBrowserBrokerRoot
	base.EgoBrowserProtocolVersion = snapshot.EgoBrowserProtocolVersion
	base.EgoBrowserWrapperVersion = snapshot.EgoBrowserWrapperVersion
	base.EgoBrowserSkillPath = snapshot.EgoBrowserSkillPath
	base.EgoBrowserSkillVersion = snapshot.EgoBrowserSkillVersion
	base.EgoBrowserSkillTreeSHA256 = snapshot.EgoBrowserSkillTreeSHA256
	return base.WithDefaults()
}

func (snapshot SessionRuntimeConfig) matches(config EngineConfig) bool {
	expected := sessionRuntimeConfigFromEngine(config)
	return snapshot == *expected
}

func readRuntimeSpec(config EngineConfig, specPath string) (SessionSpec, EngineConfig, error) {
	config = config.WithDefaults()
	spec, err := readSessionSpecFile(config, specPath)
	if err != nil {
		return SessionSpec{}, EngineConfig{}, err
	}
	runtimeConfig, err := runtimeConfigForSpec(config, spec)
	if err != nil {
		return SessionSpec{}, EngineConfig{}, err
	}
	if err := validateSessionSpec(runtimeConfig, spec, specPath); err != nil {
		return SessionSpec{}, EngineConfig{}, err
	}
	return spec, runtimeConfig, nil
}

func runtimeConfigForSpec(base EngineConfig, spec SessionSpec) (EngineConfig, error) {
	base = base.WithDefaults()
	if spec.RuntimeConfig != nil {
		snapshot := *spec.RuntimeConfig
		// These command fields were added after the first snapshot format. Fill
		// absent values from the invoking helper so old specs remain usable.
		if snapshot.RuntimeBinaryPath == "" {
			snapshot.RuntimeBinaryPath = base.RuntimeBinaryPath
		}
		if snapshot.TmuxBinaryPath == "" {
			snapshot.TmuxBinaryPath = base.TmuxBinaryPath
		}
		if snapshot.BubblewrapPath == "" {
			snapshot.BubblewrapPath = base.BubblewrapPath
		}
		if err := snapshot.validate(); err != nil {
			return EngineConfig{}, err
		}
		if filepath.Clean(snapshot.StateRoot) != filepath.Clean(base.StateRoot) {
			return EngineConfig{}, errors.New("session runtime configuration state root does not match command")
		}
		return snapshot.apply(base), nil
	}

	// Specs written before the snapshot field are still usable with the
	// deployment defaults. Their feature paths remain authoritative because
	// they were already written by the root helper.
	if root, ok := legacyWorkspaceRoot(spec.WorkspacePath, spec.UserID); ok {
		base.WorkspaceRoot = root
	}
	if root, ok := legacyAccountRoot(spec.AccountPath, spec.UserID); ok {
		base.AccountRoot = root
	}
	if spec.RuntimeRoot != "" {
		base.ClaudeRuntimePath = filepath.Join(spec.RuntimeRoot, "bin", "claude")
	}
	if spec.DeviceProxyPath != "" {
		base.DeviceProxyPath = spec.DeviceProxyPath
	}
	base.EgoBrowserEnabled = spec.EgoBrowserEnabled
	base.EgoBrowserWrapperPath = spec.EgoBrowserWrapperPath
	base.EgoBrowserBrokerSocket = spec.EgoBrowserBrokerSocket
	base.EgoBrowserProtocolVersion = spec.EgoBrowserProtocolVersion
	base.EgoBrowserWrapperVersion = spec.EgoBrowserWrapperVersion
	base.EgoBrowserSkillPath = spec.EgoBrowserSkillPath
	base.EgoBrowserSkillVersion = spec.EgoBrowserSkillVersion
	base.EgoBrowserSkillTreeSHA256 = spec.EgoBrowserSkillTreeSHA256
	return base.WithDefaults(), nil
}

func legacyWorkspaceRoot(path string, userID string) (string, bool) {
	workspaceDir := filepath.Dir(filepath.Clean(path))
	if filepath.Base(workspaceDir) == "" || filepath.Base(filepath.Dir(workspaceDir)) != "workspaces" {
		return "", false
	}
	userRoot := filepath.Dir(filepath.Dir(workspaceDir))
	if filepath.Base(userRoot) != userID {
		return "", false
	}
	return filepath.Dir(userRoot), true
}

func legacyAccountRoot(path string, userID string) (string, bool) {
	accountDir := filepath.Dir(filepath.Clean(path))
	if filepath.Base(accountDir) != "claude" || filepath.Base(filepath.Dir(accountDir)) != "tool-accounts" {
		return "", false
	}
	userRoot := filepath.Dir(filepath.Dir(accountDir))
	if filepath.Base(userRoot) != userID {
		return "", false
	}
	return filepath.Dir(userRoot), true
}

func readSessionSpecFile(config EngineConfig, specPath string) (SessionSpec, error) {
	if !pathInside(filepath.Join(config.StateRoot, "sessions"), specPath) {
		return SessionSpec{}, errors.New("spec path is outside runtime state")
	}
	info, err := os.Lstat(specPath)
	if err != nil {
		return SessionSpec{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return SessionSpec{}, errors.New("spec permissions are invalid")
	}
	if runtime.GOOS == "linux" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return SessionSpec{}, errors.New("spec is not root-owned")
		}
	}
	if info.Size() > 128*1024 {
		return SessionSpec{}, errors.New("spec is too large")
	}
	file, err := os.Open(specPath)
	if err != nil {
		return SessionSpec{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
	if err != nil {
		return SessionSpec{}, err
	}
	if len(data) > 128*1024 {
		return SessionSpec{}, errors.New("spec is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var spec SessionSpec
	if err := decoder.Decode(&spec); err != nil {
		return SessionSpec{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return SessionSpec{}, errors.New("spec contains trailing data")
	}
	return spec, nil
}

func validateSessionSpec(config EngineConfig, spec SessionSpec, specPath string) error {
	config = config.WithDefaults()
	expectedRoot := filepath.Dir(specPath)
	expectedRuntimeRoot := filepath.Clean(filepath.Join(filepath.Dir(config.ClaudeRuntimePath), ".."))
	expectedDigest := shortDigest(spec.SessionID, 12)
	if filepath.Base(specPath) != "spec.json" || filepath.Base(expectedRoot) != spec.SessionID ||
		spec.Version != ProtocolVersion ||
		!pathInside(config.WorkspaceRoot, spec.WorkspacePath) ||
		!pathInside(config.AccountRoot, spec.AccountPath) ||
		spec.SessionRoot != expectedRoot ||
		spec.TmuxSocketPath != filepath.Join(expectedRoot, "tmux", "tmux.sock") ||
		spec.RuntimeRoot != expectedRuntimeRoot ||
		spec.RuntimeCommand != "/opt/agent-remote/runtime/bin/claude" ||
		validateID(spec.SessionID, "session_id") != nil ||
		validateID(spec.UserID, "user_id") != nil ||
		validateName(spec.TmuxSessionName, "tmux_session_name") != nil ||
		spec.UnitName != "agent-remote-session-"+expectedDigest+".service" ||
		spec.NetworkNamespace != "ar-"+shortDigest(spec.SessionID, 10) ||
		spec.Username != "ar-u-"+shortDigest(spec.UserID, 12) {
		return errors.New("spec contains unmanaged paths")
	}
	if spec.DeveloperCredentialProfilePath != "" {
		profileRoot := filepath.Join(config.AccountRoot, spec.UserID, "developer-credential-profiles")
		if !pathInside(profileRoot, spec.DeveloperCredentialProfilePath) {
			return errors.New("developer credential profile path is outside managed root")
		}
	}
	if spec.SSHAgentDirectory != "" && spec.SSHAgentDirectory != filepath.Join(spec.SessionRoot, "ssh-agent") {
		return errors.New("SSH agent directory is outside session state")
	}
	if spec.DeviceControlProtocolVersion != 0 {
		expectedArgs, err := managedDeviceControlArgv(spec.SessionID, nil)
		if err != nil || spec.DeviceControlProtocolVersion != 1 ||
			spec.DeviceControlDirectory != filepath.Join(spec.SessionRoot, "device-control") ||
			filepath.Clean(spec.DeviceProxyPath) != filepath.Clean(config.DeviceProxyPath) ||
			!argumentPrefix(spec.Argv, expectedArgs) {
			return errors.New("spec contains invalid managed device control")
		}
	} else if spec.DeviceControlDirectory != "" || spec.DeviceProxyPath != "" {
		return errors.New("spec contains unconfigured device control paths")
	}
	if err := validateSpecEgoBrowserContext(config, &spec, false); err != nil {
		return err
	}
	if spec.EgoBrowserEnabled {
		if err := validateEgoBrowserArtifacts(
			spec.EgoBrowserWrapperPath,
			spec.EgoBrowserWrapperVersion,
			spec.EgoBrowserSkillPath,
			spec.EgoBrowserSkillVersion,
			spec.EgoBrowserSkillTreeSHA256,
		); err != nil {
			return err
		}
	}
	return nil
}

func validLowerHexDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// validCommandReference accepts either a managed absolute executable path or
// a simple command name resolved through the service user's PATH. It rejects
// traversal, separators in bare names, and shell-control characters.
func validCommandReference(value string) bool {
	if value == "" || filepath.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	if filepath.IsAbs(value) {
		return safeManagedPath(value)
	}
	return value != "." && value != ".." && !strings.ContainsAny(value, "/\\")
}
