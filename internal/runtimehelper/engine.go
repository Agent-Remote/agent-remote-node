package runtimehelper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/browser"
	"github.com/Agent-Remote/agent-remote-node/internal/devicecontrol"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/managedskills"
	"github.com/Agent-Remote/agent-remote-node/internal/tmuxsession"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
	"github.com/Agent-Remote/agent-remote-node/internal/wireguard"
	"github.com/Agent-Remote/agent-remote-node/internal/workspace"
)

// EngineConfig defines root-owned runtime paths and managed dependencies.
type EngineConfig struct {
	StateRoot                 string
	NodeConfigPath            string
	WorkspaceRoot             string
	AccountRoot               string
	RuntimeBinaryPath         string
	ClaudeRuntimePath         string
	DeviceProxyPath           string
	TmuxBinaryPath            string
	BubblewrapPath            string
	SystemdRunPath            string
	SystemctlPath             string
	IPPath                    string
	NFTPath                   string
	SetfaclPath               string
	MountPath                 string
	UmountPath                string
	MountpointPath            string
	DataGroup                 string
	NodeUser                  string
	DNSResolvers              []string
	DockerBinaryPath          string
	BrowserRoot               string
	BrowserImage              string
	BrowserPublicBaseURL      string
	BrowserDockerNetwork      string
	EgoBrowserEnabled         bool
	EgoBrowserWrapperPath     string
	EgoBrowserBrokerSocket    string
	EgoBrowserBrokerRoot      string
	EgoBrowserProtocolVersion string
	EgoBrowserWrapperVersion  string
	EgoBrowserSkillPath       string
	EgoBrowserSkillVersion    string
	EgoBrowserSkillTreeSHA256 string
	WireGuardInterface        string
	WireGuardPrivateKey       string
	WireGuardListenPort       int
	WGBinaryPath              string
}

// WithDefaults returns a complete helper configuration.
func (c EngineConfig) WithDefaults() EngineConfig {
	if c.StateRoot == "" {
		c.StateRoot = "/var/lib/agent-remote-runtime"
	}
	if c.NodeConfigPath == "" {
		c.NodeConfigPath = "/etc/agent-remote-node/config.json"
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = "/var/lib/agent-remote/users"
	}
	if c.AccountRoot == "" {
		c.AccountRoot = c.WorkspaceRoot
	}
	if c.RuntimeBinaryPath == "" {
		c.RuntimeBinaryPath = "/usr/local/bin/agent-remote-runtime"
	}
	if c.ClaudeRuntimePath == "" {
		c.ClaudeRuntimePath = "/opt/agent-remote/runtimes/claude/current/bin/claude"
	}
	if c.DeviceProxyPath == "" {
		c.DeviceProxyPath = "/opt/agent-remote/device/current/bin/agent-remote-device-proxy"
	}
	if c.TmuxBinaryPath == "" {
		c.TmuxBinaryPath = "tmux"
	}
	if c.BubblewrapPath == "" {
		c.BubblewrapPath = "bwrap"
	}
	if c.SystemdRunPath == "" {
		c.SystemdRunPath = "systemd-run"
	}
	if c.SystemctlPath == "" {
		c.SystemctlPath = "systemctl"
	}
	if c.IPPath == "" {
		c.IPPath = "ip"
	}
	if c.NFTPath == "" {
		c.NFTPath = "nft"
	}
	if c.SetfaclPath == "" {
		c.SetfaclPath = "setfacl"
	}
	if c.MountPath == "" {
		c.MountPath = "mount"
	}
	if c.UmountPath == "" {
		c.UmountPath = "umount"
	}
	if c.MountpointPath == "" {
		c.MountpointPath = "mountpoint"
	}
	if c.DataGroup == "" {
		c.DataGroup = "agent-remote"
	}
	if c.NodeUser == "" {
		c.NodeUser = "agent-remote"
	}
	if len(c.DNSResolvers) == 0 {
		c.DNSResolvers = []string{"1.1.1.1", "8.8.8.8"}
	}
	if c.DockerBinaryPath == "" {
		c.DockerBinaryPath = "docker"
	}
	if c.BrowserRoot == "" {
		c.BrowserRoot = "/var/lib/agent-remote/browser-sessions"
	}
	if c.BrowserImage == "" {
		c.BrowserImage = "kasmweb/chrome:1.18.0"
	}
	if c.EgoBrowserWrapperPath == "" {
		c.EgoBrowserWrapperPath = "/opt/agent-remote/ego-browser/current/bin/ego-browser"
	}
	if c.EgoBrowserBrokerSocket == "" {
		c.EgoBrowserBrokerSocket = "/run/agent-remote-node/ego-browser-broker.sock"
	}
	if c.EgoBrowserBrokerRoot == "" {
		c.EgoBrowserBrokerRoot = "/var/lib/agent-remote-node/ego-browser"
	}
	if c.EgoBrowserProtocolVersion == "" {
		c.EgoBrowserProtocolVersion = "ego-browser-bridge-v1"
	}
	if c.EgoBrowserWrapperVersion == "" {
		c.EgoBrowserWrapperVersion = egobrowserartifact.PinnedWrapperVersion
	}
	if c.EgoBrowserSkillPath == "" {
		c.EgoBrowserSkillPath = "/opt/agent-remote/ego-browser/current/skill/ego-browser"
	}
	if c.EgoBrowserSkillVersion == "" {
		c.EgoBrowserSkillVersion = "1.2.3"
	}
	if c.EgoBrowserSkillTreeSHA256 == "" {
		c.EgoBrowserSkillTreeSHA256 = "262110a09678fd3e0bbb382400588dacb98b24659b3b4a57903703b65d133c7c"
	}
	if c.WireGuardInterface == "" {
		c.WireGuardInterface = "agent-remote"
	}
	if c.WireGuardPrivateKey == "" {
		c.WireGuardPrivateKey = "/etc/agent-remote-node/wireguard.key"
	}
	if c.WireGuardListenPort <= 0 {
		c.WireGuardListenPort = 51820
	}
	if c.WGBinaryPath == "" {
		c.WGBinaryPath = "wg"
	}
	return c
}

// Engine owns privileged native-runtime lifecycle operations.
type Engine struct {
	config EngineConfig
}

// NewEngine creates a privileged runtime engine.
func NewEngine(config EngineConfig) Engine {
	return Engine{config: config.WithDefaults()}
}

// DialSessionLoopback connects to one fixed loopback port inside a managed session runtime.
func (e Engine) DialSessionLoopback(ctx context.Context, request Request) (net.Conn, error) {
	if request.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version %d", request.Version)
	}
	if err := validateID(request.RequestID, "request_id"); err != nil {
		return nil, err
	}
	data, err := json.Marshal(request.Payload)
	if err != nil {
		return nil, errors.New("dial session loopback payload is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload DialSessionLoopbackPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("dial session loopback payload is invalid")
	}
	if err := validateID(payload.SessionID, "session_id"); err != nil {
		return nil, err
	}
	if payload.Port < 1 || payload.Port > 65535 {
		return nil, errors.New("session loopback port is invalid")
	}
	switch payload.RuntimeBackend {
	case "native":
		spec, err := readTrustedSpec(e.config, e.specPath(payload.SessionID))
		if err != nil {
			return nil, fmt.Errorf("managed session is unavailable: %w", err)
		}
		return dialNetworkNamespaceLoopback(ctx, spec.NetworkNamespace, payload.Port)
	case "docker_sandbox":
		spec, err := e.loadDockerSessionSpec(payload.SessionID)
		if err != nil {
			return nil, fmt.Errorf("managed session is unavailable: %w", err)
		}
		return e.dialDockerSessionLoopback(ctx, spec, payload.Port)
	default:
		return nil, errors.New("session port forwarding backend is unsupported")
	}
}

// Execute performs one idempotent declarative operation.
func (e Engine) Execute(ctx context.Context, request Request) (map[string]any, error) {
	if request.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version %d", request.Version)
	}
	if err := validateID(request.RequestID, "request_id"); err != nil {
		return nil, err
	}
	cacheable := request.Operation != "probe" && request.Operation != "inspect_session" && request.Operation != "list_sessions" && request.Operation != "wireguard_sync"
	if cacheable {
		if cached, ok, err := e.cachedResult(request.RequestID); err != nil {
			return nil, err
		} else if ok {
			return cached, nil
		}
	}
	var result map[string]any
	var err error
	switch request.Operation {
	case "probe":
		result, err = e.probe()
	case "prepare_account":
		result, err = e.prepareAccount(ctx, request.Payload)
	case "start_session":
		result, err = e.startSession(ctx, request.Payload)
	case "stop_session":
		result, err = e.stopSession(ctx, request.Payload)
	case "inspect_session":
		result, err = e.inspectSession(request.Payload)
	case "list_sessions":
		result, err = e.listSessions()
	case "cleanup_resources":
		result, err = e.cleanupResources(ctx, request.Payload)
	case "migrate_account":
		result, err = e.migrateAccount(ctx, request.RequestID, request.Payload)
	case "docker_prepare_account":
		result, err = e.dockerPrepareAccount(request.Payload)
	case "docker_start_session":
		result, err = e.dockerStartSession(request.Payload)
	case "docker_stop_session":
		result, err = e.dockerStopSession(request.Payload)
	case "docker_start_browser":
		result, err = e.dockerStartBrowser(request.Payload)
	case "docker_stop_browser":
		result, err = e.dockerStopBrowser(request.Payload)
	case "prepare_workspace":
		result, err = e.prepareWorkspace(request.Payload)
	case "wireguard_sync":
		result, err = e.wireGuardSync(ctx, request.Payload)
	case "update_device_control_context":
		result, err = e.updateDeviceControlContext(request.Payload)
	case "clear_device_control_context":
		result, err = e.clearDeviceControlContext(request.Payload)
	default:
		err = fmt.Errorf("unsupported operation %q", request.Operation)
	}
	if err != nil {
		return nil, err
	}
	if cacheable {
		if err := e.saveResult(request.RequestID, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

type clearDeviceControlContextPayload struct {
	DeviceSessionID string `json:"device_session_id"`
	ToolSessionID   string `json:"tool_session_id"`
	Generation      uint64 `json:"generation"`
	Inclusive       bool   `json:"inclusive"`
}

type managedDeviceContext struct {
	AuthorizationMode           string   `json:"authorization_mode"`
	AuthorizationPolicyVersion  int      `json:"authorization_policy_version"`
	UserID                      string   `json:"user_id"`
	DeviceID                    string   `json:"device_id"`
	ToolSessionID               string   `json:"tool_session_id"`
	DeviceSessionID             string   `json:"device_session_id"`
	NodeID                      string   `json:"node_id"`
	Platform                    string   `json:"platform"`
	Generation                  uint64   `json:"generation"`
	NextSequence                uint64   `json:"next_sequence"`
	CurrentScreenshotGeneration uint64   `json:"current_screenshot_generation"`
	CurrentStateGeneration      uint64   `json:"current_state_generation"`
	Capabilities                []string `json:"capabilities"`
	LeaseUntil                  string   `json:"lease_until"`
}

type managedSessionContextSpec struct {
	SessionID                    string
	UserID                       string
	DeviceControlProtocolVersion int
	DeviceControlDirectory       string
	RuntimeUID                   int
	RuntimeGID                   int
}

func nativeManagedSessionContextSpec(spec SessionSpec) managedSessionContextSpec {
	return managedSessionContextSpec{
		SessionID:                    spec.SessionID,
		UserID:                       spec.UserID,
		DeviceControlProtocolVersion: spec.DeviceControlProtocolVersion,
		DeviceControlDirectory:       spec.DeviceControlDirectory,
		RuntimeUID:                   spec.RuntimeUID,
		RuntimeGID:                   spec.RuntimeGID,
	}
}

func (e Engine) loadManagedSessionContextSpec(sessionID string) (managedSessionContextSpec, error) {
	native, err := readTrustedSpec(e.config, e.specPath(sessionID))
	if err == nil {
		return nativeManagedSessionContextSpec(native), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return managedSessionContextSpec{}, err
	}
	docker, err := e.loadDockerSessionSpec(sessionID)
	if err != nil {
		return managedSessionContextSpec{}, err
	}
	if dockerSessionKind(docker) != dockerSessionKindTool {
		return managedSessionContextSpec{}, errors.New("managed Docker resource is not a tool session")
	}
	return managedSessionContextSpec{
		SessionID: docker.SessionID, UserID: docker.UserID,
		DeviceControlProtocolVersion: docker.DeviceControlProtocolVersion,
		DeviceControlDirectory:       docker.DeviceControlDirectory,
		RuntimeUID:                   docker.RuntimeUID, RuntimeGID: docker.RuntimeGID,
	}, nil
}

func (e Engine) updateDeviceControlContext(payload map[string]any) (map[string]any, error) {
	now := time.Now().UTC()
	decoded, err := devicecontrol.DecodeContextPayload(payload, "", now)
	if err != nil {
		return nil, err
	}
	spec, err := e.loadManagedSessionContextSpec(decoded.ToolSessionID)
	if err != nil {
		return nil, fmt.Errorf("load managed device-control session: %w", err)
	}
	if spec.DeviceControlProtocolVersion != 1 || spec.SessionID != decoded.ToolSessionID || spec.UserID != decoded.UserID {
		return nil, errors.New("device-control context does not match the managed session")
	}
	contextPath, err := validateDeviceControlContextPath(spec)
	if err != nil {
		return nil, err
	}
	context := managedDeviceContext{
		AuthorizationMode: decoded.AuthorizationMode, AuthorizationPolicyVersion: decoded.AuthorizationPolicyVersion,
		UserID: decoded.UserID, DeviceID: decoded.DeviceID,
		ToolSessionID: decoded.ToolSessionID, DeviceSessionID: decoded.DeviceSessionID,
		NodeID: decoded.NodeID, Platform: decoded.Platform, Generation: decoded.Generation,
		NextSequence: 1, CurrentScreenshotGeneration: 0, CurrentStateGeneration: 0,
		Capabilities: append([]string{}, decoded.Capabilities...),
		LeaseUntil:   decoded.LeaseUntil,
	}
	current, err := loadManagedDeviceContext(contextPath, spec)
	if err == nil {
		if current.Generation > context.Generation {
			return nil, errors.New("device-control context generation is stale")
		}
		if current.Generation == context.Generation {
			if !sameDeviceControlBinding(current, context) {
				return nil, errors.New("device-control binding changed within a generation")
			}
			if len(current.Capabilities) > 0 && !slices.Equal(current.Capabilities, context.Capabilities) {
				return nil, errors.New("device-control capabilities changed within a generation")
			}
			currentLease, parseCurrentErr := time.Parse(time.RFC3339Nano, current.LeaseUntil)
			newLease, parseNewErr := time.Parse(time.RFC3339Nano, context.LeaseUntil)
			if parseCurrentErr != nil || parseNewErr != nil || newLease.Before(currentLease) {
				return nil, errors.New("device-control lease moved backwards within a generation")
			}
			context.NextSequence = current.NextSequence
			context.CurrentScreenshotGeneration = current.CurrentScreenshotGeneration
			context.CurrentStateGeneration = current.CurrentStateGeneration
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := writeManagedDeviceContext(contextPath, context, spec); err != nil {
		return nil, err
	}
	return map[string]any{
		"status": "updated", "device_session_id": context.DeviceSessionID,
		"tool_session_id": context.ToolSessionID, "generation": context.Generation,
	}, nil
}

func (e Engine) clearDeviceControlContext(payload map[string]any) (map[string]any, error) {
	var decoded clearDeviceControlContextPayload
	if err := decodeStrictPayload(payload, &decoded); err != nil {
		return nil, err
	}
	if _, err := devicecontrol.DecodeDeactivatePayload(map[string]any{
		"device_session_id": decoded.DeviceSessionID,
		"tool_session_id":   decoded.ToolSessionID,
		"generation":        decoded.Generation,
	}); err != nil {
		return nil, err
	}
	spec, err := e.loadManagedSessionContextSpec(decoded.ToolSessionID)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"status": "cleared", "removed": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load managed device-control session: %w", err)
	}
	if spec.DeviceControlProtocolVersion != 1 || spec.SessionID != decoded.ToolSessionID {
		return nil, errors.New("device-control clear does not match the managed session")
	}
	contextPath, err := validateDeviceControlContextPath(spec)
	if err != nil {
		return nil, err
	}
	bridgeSocketPath := filepath.Join(spec.DeviceControlDirectory, "bridge.sock")
	current, err := loadManagedDeviceContext(contextPath, spec)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{
			"status": "cleared", "removed": false, "bridge_socket_path": bridgeSocketPath,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if current.ToolSessionID != decoded.ToolSessionID || current.DeviceSessionID != decoded.DeviceSessionID {
		return nil, errors.New("device-control clear binding does not match current context")
	}
	if current.Generation > decoded.Generation || (!decoded.Inclusive && current.Generation == decoded.Generation) {
		return map[string]any{
			"status": "cleared", "removed": false, "bridge_socket_path": bridgeSocketPath,
		}, nil
	}
	if err := os.Remove(contextPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return map[string]any{
		"status": "cleared", "removed": true, "bridge_socket_path": bridgeSocketPath,
	}, nil
}

func validateDeviceControlContextPath(spec managedSessionContextSpec) (string, error) {
	expectedDirectory := spec.DeviceControlDirectory
	if expectedDirectory == "" || filepath.Base(expectedDirectory) != "device-control" {
		return "", errors.New("device-control directory is outside managed session state")
	}
	info, err := os.Lstat(expectedDirectory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("device-control directory is unsafe")
	}
	if runtime.GOOS == "linux" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return "", errors.New("device-control directory is not root-owned")
		}
	}
	return filepath.Join(expectedDirectory, "context.json"), nil
}

func loadManagedDeviceContext(path string, spec managedSessionContextSpec) (managedDeviceContext, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return managedDeviceContext{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || info.Size() > 16*1024 {
		return managedDeviceContext{}, errors.New("device-control context file is unsafe")
	}
	if runtime.GOOS == "linux" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != spec.RuntimeUID || int(stat.Gid) != spec.RuntimeGID {
			return managedDeviceContext{}, errors.New("device-control context ownership is invalid")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return managedDeviceContext{}, err
	}
	var context managedDeviceContext
	if err := decodeStrictJSON(data, &context); err != nil {
		return managedDeviceContext{}, err
	}
	if context.AuthorizationMode == "" && context.AuthorizationPolicyVersion == 0 {
		context.AuthorizationMode = devicecontrol.AuthorizationModePerApplicationApproval
		context.AuthorizationPolicyVersion = 1
	}
	if context.Generation == 0 ||
		context.Generation > devicecontrol.MaximumActiveDeviceSessionGeneration ||
		context.NextSequence == 0 ||
		context.Platform != "macos" ||
		!devicecontrol.ValidCapabilitiesForAuthorization(
			context.AuthorizationMode,
			context.AuthorizationPolicyVersion,
			context.Capabilities,
		) {
		return managedDeviceContext{}, errors.New("device-control context data is invalid")
	}
	return context, nil
}

func writeManagedDeviceContext(path string, context managedDeviceContext, spec managedSessionContextSpec) error {
	data, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".context-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	closeWithError := func(operationError error) error {
		if closeErr := temporary.Close(); operationError == nil {
			return closeErr
		}
		return operationError
	}
	if err := temporary.Chmod(0o600); err != nil {
		return closeWithError(err)
	}
	if err := temporary.Chown(spec.RuntimeUID, spec.RuntimeGID); err != nil {
		return closeWithError(err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		return closeWithError(err)
	}
	if err := temporary.Sync(); err != nil {
		return closeWithError(err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func sameDeviceControlBinding(left managedDeviceContext, right managedDeviceContext) bool {
	return left.AuthorizationMode == right.AuthorizationMode &&
		left.AuthorizationPolicyVersion == right.AuthorizationPolicyVersion &&
		left.UserID == right.UserID && left.DeviceID == right.DeviceID &&
		left.ToolSessionID == right.ToolSessionID && left.DeviceSessionID == right.DeviceSessionID &&
		left.NodeID == right.NodeID && left.Platform == right.Platform && left.Generation == right.Generation
}

func decodeStrictPayload(payload map[string]any, destination any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return decodeStrictJSON(data, destination)
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("payload must contain one JSON object")
	}
	return nil
}

func (e Engine) wireGuardSync(ctx context.Context, payload map[string]any) (map[string]any, error) {
	decoded, err := wireguard.DecodeSyncPayload(payload)
	if err != nil {
		return nil, err
	}
	if err := wireguard.ValidateInterface(e.config.WireGuardInterface); err != nil {
		return nil, err
	}
	privateKey, err := os.ReadFile(e.config.WireGuardPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("read wireguard private key: %w", err)
	}
	rendered, err := wireguard.RenderSyncConfig(string(privateKey), e.config.WireGuardListenPort, decoded)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(e.config.StateRoot, "wireguard-sync-*.conf")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	if _, err := file.WriteString(rendered); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(ctx, e.config.WGBinaryPath, "syncconf", e.config.WireGuardInterface, path).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("wireguard sync failed: %s", strings.TrimSpace(string(output)))
	}
	return map[string]any{"status": "synchronized", "peer_count": len(decoded.Peers)}, nil
}

func (e Engine) prepareWorkspace(payload map[string]any) (map[string]any, error) {
	decoded, err := workspace.DecodePreparePayload(payload)
	if err != nil {
		return nil, err
	}
	result, err := workspace.Prepare(e.config.WorkspaceRoot, decoded)
	if err != nil {
		return nil, err
	}
	if err := e.prepareSyncedWorkspace(decoded.UserID, result.RemotePath); err != nil {
		return nil, err
	}
	return map[string]any{
		"status": "prepared", "remote_path": result.RemotePath, "marker_path": result.MarkerPath,
	}, nil
}

func (e Engine) dockerPrepareAccount(payload map[string]any) (map[string]any, error) {
	egoContext, err := parseEgoBrowserRuntimeContext(payload, e.config.WithDefaults())
	if err != nil {
		return nil, err
	}
	if egoContext.Enabled {
		return nil, errors.New("ego-browser is available only in a tool session")
	}
	decoded, err := toolaccounts.DecodeCreateBindingPayload(payload)
	if err != nil {
		return nil, err
	}
	identity, runtimeConfig, err := e.prepareDockerBindingRuntime()
	if err != nil {
		return nil, err
	}
	result, err := toolaccounts.PrepareBinding(
		e.config.AccountRoot,
		e.config.DockerBinaryPath,
		e.config.TmuxBinaryPath,
		decoded,
		runtimeConfig,
	)
	if err != nil {
		return nil, err
	}
	spec := DockerSessionSpec{
		Version: dockerSessionSpecVersion, Kind: dockerSessionKindBinding,
		SessionID: decoded.BindingID, UserID: decoded.UserID,
		TmuxSessionName: result.TmuxSessionName, SandboxName: result.ContainerName,
		BootID: currentBootID(), RuntimeUID: identity.UID, RuntimeGID: identity.GID,
	}
	if err := e.saveDockerSessionSpec(spec); err != nil {
		_, _ = toolsessions.Stop(e.config.DockerBinaryPath, e.config.TmuxBinaryPath, toolsessions.StopPayload{
			SessionID: decoded.BindingID, TmuxSessionName: result.TmuxSessionName,
			SandboxName: result.ContainerName, RuntimeBackend: "docker_sandbox",
		})
		return nil, err
	}
	return map[string]any{
		"status": result.Status, "binding_session_id": result.BindingSessionID,
		"tool_account_id": result.ToolAccountID, "tool_type": result.ToolType,
		"account_remote_path": result.AccountRemotePath, "tmux_session_name": result.TmuxSessionName,
		"container_name": result.ContainerName, "marker_path": result.MarkerPath,
		"tmux_started": result.TmuxStarted, "verifier": result.Verifier,
		"runtime_backend": "docker_sandbox", "runtime_resource_id": result.ContainerName,
	}, nil
}

func (e Engine) dockerStartSession(payload map[string]any) (map[string]any, error) {
	egoContext, err := parseEgoBrowserRuntimeContext(payload, e.config.WithDefaults())
	if err != nil {
		return nil, err
	}
	decoded, err := toolsessions.DecodeCreatePayload(payload)
	if err != nil {
		return nil, err
	}
	spec, runtimeConfig, err := e.prepareDockerSessionRuntime(payload, decoded, egoContext)
	if err != nil {
		return nil, err
	}
	result, err := toolsessions.Prepare(
		e.config.WorkspaceRoot,
		e.config.AccountRoot,
		e.config.DockerBinaryPath,
		e.config.TmuxBinaryPath,
		decoded,
		runtimeConfig,
	)
	if err != nil {
		_ = e.removeDockerSessionSpec(decoded.SessionID)
		return nil, err
	}
	if err := e.saveDockerSessionSpec(spec); err != nil {
		_, _ = toolsessions.Stop(e.config.DockerBinaryPath, e.config.TmuxBinaryPath, toolsessions.StopPayload{
			SessionID: decoded.SessionID, TmuxSessionName: result.TmuxSessionName,
			SandboxName: result.SandboxName, RuntimeBackend: "docker_sandbox",
		})
		return nil, err
	}
	response := map[string]any{
		"status": result.Status, "session_id": result.SessionID, "tool_account_id": result.ToolAccountID,
		"tool_type": result.ToolType, "workspace_remote_path": result.WorkspaceRemotePath,
		"account_remote_path": result.AccountRemotePath, "tmux_session_name": result.TmuxSessionName,
		"sandbox_name": result.SandboxName, "container_id": result.SandboxName,
		"marker_path": result.MarkerPath, "tmux_started": result.TmuxStarted,
		"runtime_backend": "docker_sandbox", "runtime_resource_id": result.SandboxName,
	}
	if egoContext.Enabled {
		response["runtime_uid"] = spec.RuntimeUID
	}
	return response, nil
}

func (e Engine) dockerStopSession(payload map[string]any) (map[string]any, error) {
	decoded, err := toolsessions.DecodeStopPayload(payload)
	if err != nil {
		return nil, err
	}
	spec, loadErr := e.loadDockerSessionSpec(decoded.SessionID)
	if loadErr == nil {
		if dockerSessionKind(spec) != dockerSessionKindTool {
			return nil, errors.New("managed Docker resource is not a tool session")
		}
		decoded.TmuxSessionName = spec.TmuxSessionName
		decoded.SandboxName = spec.SandboxName
	} else if errors.Is(loadErr, os.ErrNotExist) {
		return map[string]any{
			"status": "stopped", "session_id": decoded.SessionID,
			"tmux_session_name": "", "sandbox_name": "", "container_id": "",
			"tmux_stopped": false, "sandbox_removed": false,
			"runtime_backend": "docker_sandbox", "runtime_resource_id": "",
		}, nil
	} else {
		return nil, loadErr
	}
	result, err := toolsessions.Stop(e.config.DockerBinaryPath, e.config.TmuxBinaryPath, decoded)
	if err != nil {
		return nil, err
	}
	if err := e.removeDockerSessionSpec(decoded.SessionID); err != nil {
		return nil, err
	}
	return map[string]any{
		"status": result.Status, "session_id": result.SessionID, "tmux_session_name": result.TmuxSessionName,
		"sandbox_name": result.SandboxName, "container_id": result.SandboxName,
		"tmux_stopped": result.TmuxStopped, "sandbox_removed": result.SandboxRemoved,
		"runtime_backend": "docker_sandbox", "runtime_resource_id": result.SandboxName,
	}, nil
}

func (e Engine) dockerStartBrowser(payload map[string]any) (map[string]any, error) {
	decoded, err := browser.DecodeCreatePayload(payload)
	if err != nil {
		return nil, err
	}
	result, err := browser.Start(
		e.config.BrowserRoot,
		e.config.DockerBinaryPath,
		e.config.BrowserImage,
		e.config.BrowserPublicBaseURL,
		e.config.BrowserDockerNetwork,
		decoded,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": result.Status, "browser_session_id": result.BrowserSessionID,
		"container_id": result.ContainerID, "container_name": result.ContainerName,
		"stream_endpoint": result.StreamEndpoint, "profile_path": result.ProfilePath,
	}, nil
}

func (e Engine) dockerStopBrowser(payload map[string]any) (map[string]any, error) {
	decoded, err := browser.DecodeStopPayload(payload)
	if err != nil {
		return nil, err
	}
	result, err := browser.Stop(e.config.BrowserRoot, e.config.DockerBinaryPath, decoded)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": result.Status, "browser_session_id": result.BrowserSessionID,
		"container_name": result.ContainerName, "container_removed": result.ContainerRemoved,
		"profile_removed": result.ProfileRemoved,
	}, nil
}

func (e Engine) migrateAccount(ctx context.Context, requestID string, payload map[string]any) (map[string]any, error) {
	accountID, err := requiredText(payload, "tool_account_id")
	if err != nil {
		return nil, err
	}
	userID, err := requiredText(payload, "user_id")
	if err != nil {
		return nil, err
	}
	toolType, err := requiredText(payload, "tool_type")
	if err != nil {
		return nil, err
	}
	source, err := requiredText(payload, "source_runtime_backend")
	if err != nil {
		return nil, err
	}
	target, err := requiredText(payload, "target_runtime_backend")
	if err != nil {
		return nil, err
	}
	if toolType != "claude" || (source != "native" && source != "docker_sandbox") || (target != "native" && target != "docker_sandbox") || source == target {
		return nil, errors.New("runtime migration target is invalid")
	}
	for name, value := range map[string]string{"tool_account_id": accountID, "user_id": userID} {
		if err := validateID(value, name); err != nil {
			return nil, err
		}
	}
	accountPath := filepath.Join(e.config.AccountRoot, userID, "tool-accounts", toolType, accountID)
	if !pathInside(e.config.AccountRoot, accountPath) || !pathExists(accountPath) {
		return nil, errors.New("managed account path was not found")
	}
	backupPath := filepath.Join(e.config.StateRoot, "migrations", shortDigest(requestID, 32))
	if err := ensureRootDirectory(backupPath, 0o700); err != nil {
		return nil, err
	}
	if output, err := exec.CommandContext(ctx, "cp", "--archive", "--reflink=auto", accountPath+"/.", backupPath+"/").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("account backup failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := e.applyAccountBackendOwnership(userID, accountPath, target); err != nil {
		_ = e.applyAccountBackendOwnership(userID, accountPath, source)
		return nil, err
	}
	verification, verifyErr := toolaccounts.Verify(e.config.AccountRoot, toolaccounts.VerifyPayload{
		ToolAccountID: accountID, ToolType: toolType, UserID: userID,
		Verifier: "claude", AccountRemotePath: accountPath,
	})
	if verifyErr != nil || !verification.Verified {
		_ = e.applyAccountBackendOwnership(userID, accountPath, source)
		if verifyErr != nil {
			return nil, verifyErr
		}
		return nil, errors.New("migrated account verification failed")
	}
	return map[string]any{
		"migrated": true, "tool_account_id": accountID,
		"runtime_backend": target, "backup_path": backupPath,
	}, nil
}

func (e Engine) applyAccountBackendOwnership(userID string, accountPath string, backend string) error {
	var identity runtimeIdentity
	if backend == "native" {
		resolved, err := e.ensureIdentity(userID)
		if err != nil {
			return err
		}
		identity = resolved
	} else {
		found, err := user.Lookup(e.config.NodeUser)
		if err != nil {
			return err
		}
		uid, uidErr := strconv.Atoi(found.Uid)
		gid, gidErr := strconv.Atoi(found.Gid)
		if uidErr != nil || gidErr != nil {
			return errors.New("node worker identity is invalid")
		}
		identity = runtimeIdentity{Username: e.config.NodeUser, UID: uid, GID: gid}
	}
	if err := filepath.WalkDir(accountPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Lchown(path, identity.UID, identity.GID)
	}); err != nil {
		return err
	}
	if err := e.applyDataACL(accountPath, identity.Username); err != nil {
		return err
	}
	return e.grantManagedTraverse(accountPath, identity.Username)
}

func (e Engine) listSessions() (map[string]any, error) {
	sessions := []map[string]any{}
	bootID := currentBootID()
	root := filepath.Join(e.config.StateRoot, "sessions")
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || validateID(entry.Name(), "session_id") != nil {
			continue
		}
		spec, loadErr := e.loadSpec(entry.Name())
		if loadErr != nil {
			continue
		}
		active := exec.Command("systemctl", "is-active", "--quiet", spec.UnitName).Run() == nil
		summary := map[string]any{
			"session_id": spec.SessionID, "runtime_backend": "native",
			"runtime_resource_id": spec.UnitName, "active": active,
		}
		if sessionProcessExited(spec, active, bootID) {
			summary["exit_reason"] = "process_exited"
		}
		sessions = append(sessions, summary)
	}
	dockerRoot := filepath.Join(e.config.StateRoot, "docker-sessions")
	dockerEntries, err := os.ReadDir(dockerRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	seenDockerSessions := map[string]struct{}{}
	for _, entry := range dockerEntries {
		sessionID := entry.Name()
		if !entry.IsDir() {
			if filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			sessionID = strings.TrimSuffix(entry.Name(), ".json")
		}
		if validateID(sessionID, "session_id") != nil {
			continue
		}
		if _, exists := seenDockerSessions[sessionID]; exists {
			continue
		}
		spec, loadErr := e.loadDockerSessionSpec(sessionID)
		if loadErr != nil {
			continue
		}
		if dockerSessionKind(spec) != dockerSessionKindTool {
			continue
		}
		seenDockerSessions[sessionID] = struct{}{}
		active := exec.Command(e.config.TmuxBinaryPath, "has-session", "-t", spec.TmuxSessionName).Run() == nil
		summary := map[string]any{
			"session_id": spec.SessionID, "runtime_backend": "docker_sandbox",
			"runtime_resource_id": spec.SandboxName, "active": active,
		}
		if dockerSessionProcessExited(spec, active, bootID) {
			summary["exit_reason"] = "process_exited"
		}
		sessions = append(sessions, summary)
	}
	return map[string]any{"sessions": sessions}, nil
}

func currentBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func processExitMarkerPath(spec SessionSpec) string {
	return filepath.Join(spec.SessionRoot, "process-exited")
}

func sessionProcessExited(spec SessionSpec, active bool, bootID string) bool {
	if active || bootID == "" || spec.BootID != bootID {
		return false
	}
	data, err := os.ReadFile(processExitMarkerPath(spec))
	return err == nil && strings.TrimSpace(string(data)) == bootID
}

func (e Engine) cleanupResources(ctx context.Context, payload map[string]any) (map[string]any, error) {
	backend, err := requiredText(payload, "runtime_backend")
	if err != nil {
		return nil, err
	}
	if backend != "native" && backend != "docker_sandbox" {
		return nil, fmt.Errorf("cleanup_resources backend is unsupported: %q", backend)
	}
	rawIDs, ok := payload["session_ids"].([]any)
	if !ok || len(rawIDs) == 0 {
		return nil, errors.New("session_ids must contain at least one session ID")
	}
	if len(rawIDs) > 100 {
		return nil, errors.New("session_ids exceeds the maximum batch size of 100")
	}
	cleaned := make([]string, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		sessionID, ok := rawID.(string)
		if !ok || validateID(sessionID, "session_id") != nil {
			return nil, errors.New("session_ids contains an invalid session ID")
		}
		var cleanupErr error
		if backend == "native" {
			_, cleanupErr = e.stopSession(ctx, map[string]any{"session_id": sessionID})
		} else {
			_, cleanupErr = e.dockerStopSession(map[string]any{
				"session_id": sessionID, "runtime_backend": backend,
			})
		}
		if cleanupErr != nil {
			return nil, fmt.Errorf("clean session %s: %w", sessionID, cleanupErr)
		}
		cleaned = append(cleaned, sessionID)
	}
	return map[string]any{
		"status": "cleaned", "runtime_backend": backend,
		"session_ids": cleaned, "cleaned_count": len(cleaned),
	}, nil
}

func (e Engine) probe() (map[string]any, error) {
	_, dockerIdentityError := e.dockerRuntimeIdentity()
	nativeChecks := map[string]bool{
		"linux":           runtime.GOOS == "linux",
		"kernel_5_15":     kernelAtLeast(5, 15),
		"root":            os.Geteuid() == 0,
		"cgroup_v2":       pathExists("/sys/fs/cgroup/cgroup.controllers"),
		"bwrap":           commandAvailable(e.config.BubblewrapPath),
		"bwrap_self_test": commandSucceeds(e.config.BubblewrapPath, "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--unshare-user", "true"),
		"systemd_run":     commandAvailable(e.config.SystemdRunPath),
		"systemd_249":     systemdAtLeast(e.config.SystemdRunPath, 249),
		"ip":              commandAvailable(e.config.IPPath),
		"nft":             commandAvailable(e.config.NFTPath),
		"setfacl":         commandAvailable(e.config.SetfaclPath),
		"mount":           commandAvailable(e.config.MountPath),
		"umount":          commandAvailable(e.config.UmountPath),
		"mountpoint":      commandAvailable(e.config.MountpointPath),
		"tmux":            commandAvailable(e.config.TmuxBinaryPath),
		"git":             commandAvailable("git"),
		"gh":              commandAvailable("gh"),
		"ssh_client":      commandAvailable("ssh"),
		"claude_runtime":  executableExists(e.config.ClaudeRuntimePath),
		"nodejs_runtime":  executableExists(filepath.Join(filepath.Dir(e.config.ClaudeRuntimePath), "node")),
		"locale":          localeAvailable("en_US.UTF-8"),
		"network_ns":      pathExists("/proc/self/ns/net"),
		"tun":             pathExists("/dev/net/tun"),
		"disk_watermark":  diskAvailableAt(e.config.StateRoot, 2<<30),
	}
	nativeOK := true
	for _, available := range nativeChecks {
		nativeOK = nativeOK && available
	}
	dockerChecks := map[string]bool{
		"linux":            runtime.GOOS == "linux",
		"root":             os.Geteuid() == 0,
		"docker":           commandAvailable(e.config.DockerBinaryPath),
		"daemon":           commandSucceeds(e.config.DockerBinaryPath, "info"),
		"docker_sandbox":   commandSucceeds(e.config.DockerBinaryPath, "sandbox", "--help"),
		"tmux":             commandAvailable(e.config.TmuxBinaryPath),
		"git":              commandAvailable("git"),
		"setfacl":          commandAvailable(e.config.SetfaclPath),
		"runtime_identity": dockerIdentityError == nil,
	}
	dockerOK := true
	for _, available := range dockerChecks {
		dockerOK = dockerOK && available
	}
	backends := []string{}
	if dockerOK {
		backends = append(backends, "docker_sandbox")
	}
	if nativeOK {
		backends = append(backends, "native")
	}
	return map[string]any{
		"available": nativeOK || dockerOK, "backends": backends,
		"native": nativeChecks, "docker_sandbox": dockerChecks,
		"browser_docker": map[string]bool{"docker": dockerChecks["docker"], "daemon": dockerChecks["daemon"]},
		"dependencies":   dependencyDetails(e.config),
	}, nil
}

func diskAvailableAt(path string, minimumBytes uint64) bool {
	var stats syscall.Statfs_t
	if syscall.Statfs(path, &stats) != nil {
		return false
	}
	return stats.Bavail*uint64(stats.Bsize) >= minimumBytes
}

func dependencyDetails(config EngineConfig) map[string]string {
	details := map[string]string{
		"kernel":  firstCommandLine("uname", "-r"),
		"systemd": firstCommandLine(config.SystemdRunPath, "--version"),
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				details["distribution"] = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}
	runtimeRoot := filepath.Dir(filepath.Dir(config.ClaudeRuntimePath))
	for key, name := range map[string]string{
		"claude_version": "VERSION", "claude_checksum": "SHA256SUMS",
		"nodejs_version": "NODE_VERSION", "nodejs_checksum": "NODE_SHA256SUMS",
	} {
		if data, err := os.ReadFile(filepath.Join(runtimeRoot, name)); err == nil {
			details[key] = strings.TrimSpace(string(data))
		}
	}
	return details
}

func firstCommandLine(binary string, args ...string) string {
	output, err := exec.Command(binary, args...).Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	return line
}

func (e Engine) prepareAccount(ctx context.Context, payload map[string]any) (map[string]any, error) {
	bindingID, err := requiredText(payload, "binding_id")
	if err != nil {
		return nil, err
	}
	accountID, err := requiredText(payload, "tool_account_id")
	if err != nil {
		return nil, err
	}
	userID, err := requiredText(payload, "user_id")
	if err != nil {
		return nil, err
	}
	for name, value := range map[string]string{"binding_id": bindingID, "tool_account_id": accountID, "user_id": userID} {
		if err := validateID(value, name); err != nil {
			return nil, err
		}
	}
	accountPath := filepath.Join(e.config.AccountRoot, userID, "tool-accounts", "claude", accountID)
	workspacePath := filepath.Join(accountPath, "workspace")
	if err := e.prepareOwnedDirectories(userID, accountPath, workspacePath, filepath.Join(accountPath, ".claude")); err != nil {
		return nil, err
	}
	if err := ensureOwnedFile(filepath.Join(accountPath, ".claude.json"), []byte("{}\n"), 0o600, e.identity(userID)); err != nil {
		return nil, err
	}
	if err := e.installManagedClaudeSkills(userID, accountPath); err != nil {
		return nil, err
	}
	argv, err := claudeBindingArgv(payload)
	if err != nil {
		return nil, err
	}
	spec, err := e.buildSpec(payload, bindingID, userID, accountID, workspacePath, accountPath, argv, "binding")
	if err != nil {
		return nil, err
	}
	if err := e.launch(ctx, spec); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":              "waiting_user_login",
		"binding_session_id":  bindingID,
		"tool_account_id":     accountID,
		"tool_type":           "claude",
		"account_remote_path": accountPath,
		"tmux_session_name":   spec.TmuxSessionName,
		"container_name":      "",
		"tmux_started":        true,
		"verifier":            "claude",
		"runtime_backend":     "native",
		"runtime_resource_id": spec.UnitName,
	}, nil
}

func (e Engine) startSession(ctx context.Context, payload map[string]any) (map[string]any, error) {
	sessionID, err := requiredText(payload, "session_id")
	if err != nil {
		return nil, err
	}
	accountID, err := requiredText(payload, "tool_account_id")
	if err != nil {
		return nil, err
	}
	userID, err := requiredText(payload, "user_id")
	if err != nil {
		return nil, err
	}
	workspaceID, err := requiredText(payload, "workspace_id")
	if err != nil {
		return nil, err
	}
	for name, value := range map[string]string{"session_id": sessionID, "tool_account_id": accountID, "user_id": userID, "workspace_id": workspaceID} {
		if err := validateID(value, name); err != nil {
			return nil, err
		}
	}
	workspacePath := filepath.Join(e.config.WorkspaceRoot, userID, "workspaces", workspaceID, "files")
	accountPath := filepath.Join(e.config.AccountRoot, userID, "tool-accounts", "claude", accountID)
	if err := e.prepareSyncedWorkspace(userID, workspacePath); err != nil {
		return nil, err
	}
	if err := e.ensureIndependentGitIndex(userID, workspacePath); err != nil {
		return nil, err
	}
	if err := e.prepareOwnedDirectories(userID, accountPath, filepath.Join(accountPath, ".claude")); err != nil {
		return nil, err
	}
	if err := e.installManagedClaudeSkills(userID, accountPath); err != nil {
		return nil, err
	}
	argv := textList(payload["argv"])
	spec, err := e.buildSpec(payload, sessionID, userID, accountID, workspacePath, accountPath, argv, "session")
	if err != nil {
		return nil, err
	}
	if err := e.launch(ctx, spec); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":                "running",
		"session_id":            sessionID,
		"tool_account_id":       accountID,
		"tool_type":             "claude",
		"workspace_remote_path": workspacePath,
		"account_remote_path":   accountPath,
		"tmux_session_name":     spec.TmuxSessionName,
		"sandbox_name":          "",
		"container_id":          "",
		"tmux_started":          true,
		"runtime_backend":       "native",
		"runtime_resource_id":   spec.UnitName,
		"runtime_uid":           spec.RuntimeUID,
	}, nil
}

func (e Engine) stopSession(ctx context.Context, payload map[string]any) (map[string]any, error) {
	sessionID, err := requiredText(payload, "session_id")
	if err != nil {
		return nil, err
	}
	if err := validateID(sessionID, "session_id"); err != nil {
		return nil, err
	}
	spec, err := e.loadSpec(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"status": "stopped", "session_id": sessionID, "runtime_backend": "native", "runtime_resource_id": ""}, nil
		}
		return nil, err
	}
	_ = runCommand(ctx, "systemctl", "stop", spec.UnitName)
	_ = runCommand(ctx, e.config.IPPath, "netns", "delete", spec.NetworkNamespace)
	if err := e.cleanupTemp(ctx, spec); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(spec.SessionRoot); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":              "stopped",
		"session_id":          sessionID,
		"tmux_session_name":   spec.TmuxSessionName,
		"tmux_stopped":        true,
		"sandbox_removed":     false,
		"runtime_backend":     "native",
		"runtime_resource_id": spec.UnitName,
	}, nil
}

func (e Engine) inspectSession(payload map[string]any) (map[string]any, error) {
	sessionID, err := requiredText(payload, "session_id")
	if err != nil {
		return nil, err
	}
	backend := optionalText(payload, "runtime_backend", "native")
	switch backend {
	case "native":
		spec, err := e.loadSpec(sessionID)
		if err != nil {
			return nil, err
		}
		active := exec.Command(e.config.SystemctlPath, "is-active", "--quiet", spec.UnitName).Run() == nil
		return map[string]any{
			"session_id": sessionID, "active": active, "runtime_backend": backend,
			"runtime_resource_id": spec.UnitName,
		}, nil
	case "docker_sandbox":
		spec, err := e.loadDockerSessionSpec(sessionID)
		if err != nil {
			return nil, err
		}
		if dockerSessionKind(spec) != dockerSessionKindTool {
			return nil, errors.New("managed Docker resource is not a tool session")
		}
		active := exec.Command(e.config.TmuxBinaryPath, "has-session", "-t", spec.TmuxSessionName).Run() == nil
		return map[string]any{
			"session_id": sessionID, "active": active, "runtime_backend": backend,
			"runtime_resource_id": spec.SandboxName,
		}, nil
	default:
		return nil, errors.New("inspect_session backend is unsupported")
	}
}

// SessionSpec is the root-validated execution manifest consumed by unprivileged subcommands.
type SessionSpec struct {
	Version                        int      `json:"version"`
	Kind                           string   `json:"kind"`
	SessionID                      string   `json:"session_id"`
	UserID                         string   `json:"user_id"`
	Username                       string   `json:"username"`
	WorkspacePath                  string   `json:"workspace_path"`
	AccountPath                    string   `json:"account_path"`
	DeveloperCredentialProfilePath string   `json:"developer_credential_profile_path,omitempty"`
	GitHubCLIMode                  string   `json:"github_cli_mode,omitempty"`
	SSHMode                        string   `json:"ssh_mode,omitempty"`
	SSHAgentDirectory              string   `json:"ssh_agent_directory,omitempty"`
	SessionRoot                    string   `json:"session_root"`
	RuntimeRoot                    string   `json:"runtime_root"`
	RuntimeCommand                 string   `json:"runtime_command"`
	Argv                           []string `json:"argv"`
	DeviceControlProtocolVersion   int      `json:"device_control_protocol_version,omitempty"`
	DeviceControlDirectory         string   `json:"device_control_directory,omitempty"`
	DeviceProxyPath                string   `json:"device_proxy_path,omitempty"`
	Timezone                       string   `json:"timezone"`
	Locale                         string   `json:"locale"`
	TmuxSessionName                string   `json:"tmux_session_name"`
	TmuxSocketPath                 string   `json:"tmux_socket_path"`
	UnitName                       string   `json:"unit_name"`
	NetworkNamespace               string   `json:"network_namespace"`
	CreatedAt                      string   `json:"created_at"`
	BootID                         string   `json:"boot_id,omitempty"`
	RuntimeUID                     int      `json:"runtime_uid"`
	RuntimeGID                     int      `json:"runtime_gid"`
	// Ego-browser values are root-validated session identity/configuration.
	// The broker nonce is deliberately process-only and must never be persisted.
	EgoBrowserEnabled         bool   `json:"ego_browser_enabled,omitempty"`
	EgoBrowserWrapperPath     string `json:"ego_browser_wrapper_path,omitempty"`
	EgoBrowserBrokerSocket    string `json:"ego_browser_broker_socket,omitempty"`
	EgoBrowserBrokerNonce     string `json:"-"`
	EgoBrowserProtocolVersion string `json:"ego_browser_protocol_version,omitempty"`
	EgoBrowserWrapperVersion  string `json:"ego_browser_wrapper_version,omitempty"`
	EgoBrowserSkillPath       string `json:"ego_browser_skill_path,omitempty"`
	EgoBrowserSkillVersion    string `json:"ego_browser_skill_version,omitempty"`
	EgoBrowserSkillTreeSHA256 string `json:"ego_browser_skill_tree_sha256,omitempty"`
	EgoBrowserTaskSpace       string `json:"ego_browser_task_space,omitempty"`
	// RuntimeConfig is a non-sensitive snapshot used by the unprivileged
	// supervisor and exec child; it never contains node credentials.
	RuntimeConfig *SessionRuntimeConfig `json:"runtime_config,omitempty"`
	Policy        RuntimePolicy         `json:"policy"`
}

// RuntimePolicy contains root-validated per-session resource and network limits.
type RuntimePolicy struct {
	MemoryHighBytes  int64    `json:"memory_high_bytes"`
	MemoryMaxBytes   int64    `json:"memory_max_bytes"`
	CPUQuotaPercent  int64    `json:"cpu_quota_percent"`
	TasksMax         int64    `json:"tasks_max"`
	LimitNOFILE      int64    `json:"limit_nofile"`
	TmpfsSizeBytes   int64    `json:"tmpfs_size_bytes"`
	NetworkAllowlist []string `json:"network_allowlist"`
}

var defaultRuntimePolicy = RuntimePolicy{
	MemoryHighBytes: 3 << 30, MemoryMaxBytes: 4 << 30, CPUQuotaPercent: 200,
	TasksMax: 512, LimitNOFILE: 8192, TmpfsSizeBytes: 1 << 30,
}

func (e Engine) buildSpec(payload map[string]any, sessionID string, userID string, accountID string, workspacePath string, accountPath string, argv []string, kind string) (SessionSpec, error) {
	e.config = e.config.WithDefaults()
	egoContext, err := parseEgoBrowserRuntimeContext(payload, e.config)
	if err != nil {
		return SessionSpec{}, err
	}
	if egoContext.Enabled {
		if err := validateEgoBrowserArtifacts(
			egoContext.WrapperPath,
			egoContext.WrapperVersion,
			egoContext.SkillPath,
			egoContext.SkillVersion,
			egoContext.SkillTreeSHA256,
		); err != nil {
			return SessionSpec{}, err
		}
	}
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return SessionSpec{}, err
	}
	digest := shortDigest(sessionID, 12)
	sessionRoot := filepath.Join(e.config.StateRoot, "sessions", sessionID)
	if err := ensureRootDirectory(sessionRoot, 0o711); err != nil {
		return SessionSpec{}, err
	}
	tmuxRoot := filepath.Join(sessionRoot, "tmux")
	if err := os.Mkdir(tmuxRoot, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return SessionSpec{}, err
	}
	if runtime.GOOS == "linux" {
		if err := os.Chown(tmuxRoot, identity.UID, identity.GID); err != nil {
			return SessionSpec{}, err
		}
	}
	developerProfilePath, githubCLIMode, sshMode, err := e.prepareDeveloperCredentialProfile(userID, payload)
	if err != nil {
		return SessionSpec{}, err
	}
	sshAgentDirectory := ""
	if sshMode == "agent_forwarding" {
		sshAgentDirectory = filepath.Join(sessionRoot, "ssh-agent")
		if err := ensureRootDirectory(sshAgentDirectory, 0o711); err != nil {
			return SessionSpec{}, err
		}
	}
	if err := writeRuntimeIdentityFiles(sessionRoot, identity); err != nil {
		return SessionSpec{}, err
	}
	if err := writeOwnedFile(filepath.Join(sessionRoot, "process-exited"), nil, 0o600, identity); err != nil {
		return SessionSpec{}, err
	}
	timezone := optionalText(payload, "timezone", "UTC")
	locale := optionalText(payload, "locale", "en_US.UTF-8")
	if !safeTimezone(timezone) || !safeLocale(locale) || !localeAvailable(locale) {
		return SessionSpec{}, errors.New("timezone or locale is invalid")
	}
	tmuxName := optionalText(payload, "tmux_session_name", "ar-native-"+digest)
	if err := validateName(tmuxName, "tmux_session_name"); err != nil {
		return SessionSpec{}, err
	}
	runtimeRoot := filepath.Clean(filepath.Join(filepath.Dir(e.config.ClaudeRuntimePath), ".."))
	policy, err := parseRuntimePolicy(payload["runtime_policy"])
	if err != nil {
		return SessionSpec{}, err
	}
	deviceControlProtocol, err := parseDeviceControlProtocol(payload["device_control"], kind)
	if err != nil {
		return SessionSpec{}, err
	}
	deviceControlDirectory := ""
	deviceProxyPath := ""
	if deviceControlProtocol != 0 {
		if err := validateManagedDeviceProxy(e.config.DeviceProxyPath); err != nil {
			return SessionSpec{}, err
		}
		argv, err = managedDeviceControlArgv(sessionID, argv)
		if err != nil {
			return SessionSpec{}, err
		}
		deviceControlDirectory = filepath.Join(sessionRoot, "device-control")
		if err := os.Mkdir(deviceControlDirectory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return SessionSpec{}, err
		}
		if err := e.applyDeviceControlACL(deviceControlDirectory, identity.Username); err != nil {
			return SessionSpec{}, err
		}
		deviceProxyPath = filepath.Clean(e.config.DeviceProxyPath)
	}
	spec := SessionSpec{
		Version:                        ProtocolVersion,
		Kind:                           kind,
		SessionID:                      sessionID,
		UserID:                         userID,
		Username:                       identity.Username,
		WorkspacePath:                  workspacePath,
		AccountPath:                    accountPath,
		DeveloperCredentialProfilePath: developerProfilePath,
		GitHubCLIMode:                  githubCLIMode,
		SSHMode:                        sshMode,
		SSHAgentDirectory:              sshAgentDirectory,
		SessionRoot:                    sessionRoot,
		RuntimeRoot:                    runtimeRoot,
		RuntimeCommand:                 "/opt/agent-remote/runtime/bin/claude",
		Argv:                           argv,
		DeviceControlProtocolVersion:   deviceControlProtocol,
		DeviceControlDirectory:         deviceControlDirectory,
		DeviceProxyPath:                deviceProxyPath,
		Timezone:                       timezone,
		Locale:                         locale,
		TmuxSessionName:                tmuxName,
		TmuxSocketPath:                 filepath.Join(tmuxRoot, "tmux.sock"),
		UnitName:                       "agent-remote-session-" + digest + ".service",
		NetworkNamespace:               "ar-" + shortDigest(sessionID, 10),
		CreatedAt:                      time.Now().UTC().Format(time.RFC3339),
		BootID:                         currentBootID(),
		RuntimeUID:                     identity.UID,
		RuntimeGID:                     identity.GID,
		EgoBrowserEnabled:              egoContext.Enabled,
		EgoBrowserWrapperPath:          egoContext.WrapperPath,
		EgoBrowserBrokerSocket:         egoContext.BrokerSocket,
		EgoBrowserBrokerNonce:          egoContext.BrokerNonce,
		EgoBrowserProtocolVersion:      egoContext.Protocol,
		EgoBrowserWrapperVersion:       egoContext.WrapperVersion,
		EgoBrowserSkillPath:            egoContext.SkillPath,
		EgoBrowserSkillVersion:         egoContext.SkillVersion,
		EgoBrowserSkillTreeSHA256:      egoContext.SkillTreeSHA256,
		EgoBrowserTaskSpace:            egoContext.TaskSpace,
		RuntimeConfig:                  sessionRuntimeConfigFromEngine(e.config),
		Policy:                         policy,
	}
	if err := e.saveSpec(spec); err != nil {
		return SessionSpec{}, err
	}
	if err := e.grantSpecAccess(spec); err != nil {
		return SessionSpec{}, err
	}
	return spec, nil
}

func (e Engine) prepareDeveloperCredentialProfile(userID string, payload map[string]any) (string, string, string, error) {
	raw, ok := payload["developer_credentials"].(map[string]any)
	if !ok {
		if payload["developer_credentials"] == nil {
			return "", "", "", nil
		}
		return "", "", "", errors.New("developer_credentials is invalid")
	}
	profileID, _ := raw["profile_id"].(string)
	if err := validateID(profileID, "developer_credentials.profile_id"); err != nil {
		return "", "", "", err
	}
	githubCLIMode, _ := raw["gh_mode"].(string)
	if githubCLIMode != "remote_login" && githubCLIMode != "import_token" && githubCLIMode != "disabled" {
		return "", "", "", errors.New("developer_credentials.gh_mode is invalid")
	}
	sshMode, _ := raw["ssh_mode"].(string)
	if sshMode != "agent_forwarding" && sshMode != "deploy_key" && sshMode != "disabled" {
		return "", "", "", errors.New("developer_credentials.ssh_mode is invalid")
	}
	profilePath := filepath.Join(e.config.AccountRoot, userID, "developer-credential-profiles", profileID)
	if declared, _ := payload["developer_credential_profile_path"].(string); declared != "" && filepath.Clean(declared) != profilePath {
		return "", "", "", errors.New("developer credential profile path does not match managed path")
	}
	paths := []string{profilePath, filepath.Join(profilePath, "home"), filepath.Join(profilePath, "gh"), filepath.Join(profilePath, ".ssh")}
	if err := e.preparePrivateDirectories(userID, paths...); err != nil {
		return "", "", "", err
	}
	gitIdentity, _ := raw["git_identity"].(map[string]any)
	gitConfig, err := renderGitConfig(gitIdentity)
	if err != nil {
		return "", "", "", err
	}
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return "", "", "", err
	}
	if err := writeOwnedFile(filepath.Join(profilePath, "home", ".gitconfig"), []byte(gitConfig), 0o600, identity); err != nil {
		return "", "", "", err
	}
	return profilePath, githubCLIMode, sshMode, nil
}

func renderGitConfig(identity map[string]any) (string, error) {
	name, _ := identity["user_name"].(string)
	email, _ := identity["user_email"].(string)
	if err := validateGitIdentityValue(name); err != nil {
		return "", fmt.Errorf("git user name is invalid: %w", err)
	}
	if err := validateGitIdentityValue(email); err != nil {
		return "", fmt.Errorf("git user email is invalid: %w", err)
	}
	if name == "" && email == "" {
		return "", nil
	}
	lines := []string{"[user]"}
	if name != "" {
		lines = append(lines, "\tname = \""+escapeGitConfigValue(name)+"\"")
	}
	if email != "" {
		lines = append(lines, "\temail = \""+escapeGitConfigValue(email)+"\"")
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func validateGitIdentityValue(value string) error {
	if len(value) > 320 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("value contains unsupported characters")
	}
	return nil
}

func escapeGitConfigValue(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value)
}

func writeRuntimeIdentityFiles(sessionRoot string, identity runtimeIdentity) error {
	passwd := fmt.Sprintf("%s:x:%d:%d:agent-remote runtime:/home/runtime:/usr/sbin/nologin\n", identity.Username, identity.UID, identity.GID)
	group := fmt.Sprintf("%s:x:%d:\n", identity.Username, identity.GID)
	if err := writeRuntimeIdentityFile(filepath.Join(sessionRoot, "passwd"), passwd); err != nil {
		return err
	}
	return writeRuntimeIdentityFile(filepath.Join(sessionRoot, "group"), group)
}

func writeRuntimeIdentityFile(path string, contents string) error {
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func (e Engine) grantSpecAccess(spec SessionSpec) error {
	parents := []string{e.config.StateRoot, filepath.Join(e.config.StateRoot, "sessions")}
	access := "u:" + spec.Username + ":--x,u:" + e.config.NodeUser + ":--x"
	args := append([]string{"-m", access}, parents...)
	if output, err := exec.Command(e.config.SetfaclPath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("grant runtime spec access: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e Engine) launch(ctx context.Context, spec SessionSpec) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("native runtime helper requires root on Linux")
	}
	if err := e.setupTemp(ctx, spec); err != nil {
		return err
	}
	if err := e.setupNetwork(ctx, spec); err != nil {
		_ = e.cleanupTemp(ctx, spec)
		return err
	}
	properties := []string{
		"--property=NoNewPrivileges=yes",
		"--property=PrivateDevices=yes",
		"--property=ProtectKernelTunables=yes",
		"--property=ProtectKernelModules=yes",
		"--property=ProtectControlGroups=yes",
		"--property=ProtectClock=yes",
		"--property=RestrictSUIDSGID=yes",
		"--property=LockPersonality=yes",
		"--property=CapabilityBoundingSet=",
		"--property=AmbientCapabilities=",
		fmt.Sprintf("--property=MemoryHigh=%d", spec.Policy.MemoryHighBytes),
		fmt.Sprintf("--property=MemoryMax=%d", spec.Policy.MemoryMaxBytes),
		fmt.Sprintf("--property=CPUQuota=%d%%", spec.Policy.CPUQuotaPercent),
		fmt.Sprintf("--property=TasksMax=%d", spec.Policy.TasksMax),
		fmt.Sprintf("--property=LimitNOFILE=%d", spec.Policy.LimitNOFILE),
		"--property=LimitCORE=0",
		"--property=KillMode=control-group",
		"--property=NetworkNamespacePath=/run/netns/" + spec.NetworkNamespace,
	}
	args := []string{"--unit", spec.UnitName, "--collect", "--quiet", "--service-type=exec", "--uid", spec.Username}
	args = append(args, properties...)
	if spec.EgoBrowserEnabled {
		for _, entry := range egoBrowserEnvironment(spec, false) {
			args = append(args, "--setenv="+entry)
		}
	}
	args = append(args, e.config.RuntimeBinaryPath, "supervise", "--state-root", e.config.StateRoot, "--spec", e.specPath(spec.SessionID))
	if output, err := exec.CommandContext(ctx, e.config.SystemdRunPath, args...).CombinedOutput(); err != nil {
		_ = runCommand(ctx, e.config.IPPath, "netns", "delete", spec.NetworkNamespace)
		_ = e.cleanupTemp(ctx, spec)
		return fmt.Errorf("systemd-run failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := e.waitForSessionReady(ctx, spec); err != nil {
		_ = exec.CommandContext(ctx, e.config.SystemctlPath, "stop", spec.UnitName).Run()
		_ = runCommand(ctx, e.config.IPPath, "netns", "delete", spec.NetworkNamespace)
		_ = e.cleanupTemp(ctx, spec)
		return err
	}
	return nil
}

func (e Engine) waitForSessionReady(ctx context.Context, spec SessionSpec) error {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	readyChecks := 0
	for {
		stateOutput, _ := exec.CommandContext(ctx, e.config.SystemctlPath, "is-active", spec.UnitName).CombinedOutput()
		state := strings.TrimSpace(string(stateOutput))
		if state == "failed" || state == "inactive" || state == "deactivating" || state == "unknown" {
			return fmt.Errorf("native runtime unit %s became %s before tmux was ready", spec.UnitName, state)
		}
		if state == "active" && exec.CommandContext(ctx, e.config.TmuxBinaryPath, "-S", spec.TmuxSocketPath, "has-session", "-t", spec.TmuxSessionName).Run() == nil {
			readyChecks++
			if readyChecks >= 10 {
				return nil
			}
		} else {
			readyChecks = 0
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("native runtime unit %s did not become ready", spec.UnitName)
		case <-ticker.C:
		}
	}
}

func (e Engine) setupTemp(ctx context.Context, spec SessionSpec) error {
	tempPath := filepath.Join(spec.SessionRoot, "tmp")
	if err := os.MkdirAll(tempPath, 0o700); err != nil {
		return err
	}
	options := fmt.Sprintf("size=%d,mode=0700,uid=%d,gid=%d,nosuid,nodev", spec.Policy.TmpfsSizeBytes, spec.RuntimeUID, spec.RuntimeGID)
	if err := runCommand(ctx, e.config.MountPath, "-t", "tmpfs", "-o", options, "tmpfs", tempPath); err != nil {
		return fmt.Errorf("create limited session tmpfs: %w", err)
	}
	return nil
}

func (e Engine) cleanupTemp(ctx context.Context, spec SessionSpec) error {
	tempPath := filepath.Join(spec.SessionRoot, "tmp")
	if !pathExists(tempPath) {
		return nil
	}
	if err := runCommand(ctx, e.config.UmountPath, tempPath); err != nil {
		if !commandSucceeds(e.config.MountpointPath, "--quiet", tempPath) {
			return nil
		}
		return fmt.Errorf("unmount session tmpfs: %w", err)
	}
	return nil
}

func (e Engine) setupNetwork(ctx context.Context, spec SessionSpec) error {
	if err := e.ensureHostNetworking(ctx); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(spec.SessionID))
	third := int(digest[0])
	block := int(digest[1]&0x3f) * 4
	hostCIDR := fmt.Sprintf("10.231.%d.%d/30", third, block+1)
	peerCIDR := fmt.Sprintf("10.231.%d.%d/30", third, block+2)
	gateway := fmt.Sprintf("10.231.%d.%d", third, block+1)
	hostVeth := "arh" + shortDigest(spec.SessionID, 8)
	peerVeth := "arp" + shortDigest(spec.SessionID, 8)
	commands := [][]string{
		{e.config.IPPath, "netns", "add", spec.NetworkNamespace},
		{e.config.IPPath, "link", "add", hostVeth, "type", "veth", "peer", "name", peerVeth},
		{e.config.IPPath, "link", "set", peerVeth, "netns", spec.NetworkNamespace},
		{e.config.IPPath, "addr", "add", hostCIDR, "dev", hostVeth},
		{e.config.IPPath, "link", "set", hostVeth, "up"},
		{e.config.IPPath, "-n", spec.NetworkNamespace, "addr", "add", peerCIDR, "dev", peerVeth},
		{e.config.IPPath, "-n", spec.NetworkNamespace, "link", "set", "lo", "up"},
		{e.config.IPPath, "-n", spec.NetworkNamespace, "link", "set", peerVeth, "up"},
		{e.config.IPPath, "-n", spec.NetworkNamespace, "route", "add", "default", "via", gateway},
	}
	for _, command := range commands {
		if err := runCommand(ctx, command[0], command[1:]...); err != nil {
			_ = runCommand(ctx, e.config.IPPath, "netns", "delete", spec.NetworkNamespace)
			return err
		}
	}
	if err := e.applyNamespaceFirewall(ctx, spec); err != nil {
		_ = runCommand(ctx, e.config.IPPath, "netns", "delete", spec.NetworkNamespace)
		return err
	}
	return nil
}

func (e Engine) ensureHostNetworking(ctx context.Context) error {
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
		return fmt.Errorf("enable IPv4 forwarding: %w", err)
	}
	if exec.CommandContext(ctx, e.config.NFTPath, "list", "table", "ip", "agent_remote").Run() != nil {
		if err := runCommand(ctx, e.config.NFTPath, "add", "table", "ip", "agent_remote"); err != nil {
			return err
		}
	}
	if exec.CommandContext(ctx, e.config.NFTPath, "list", "chain", "ip", "agent_remote", "postrouting").Run() != nil {
		if err := runCommand(ctx, e.config.NFTPath, "add", "chain", "ip", "agent_remote", "postrouting", "{", "type", "nat", "hook", "postrouting", "priority", "srcnat", ";", "policy", "accept", ";", "}"); err != nil {
			return err
		}
	}
	output, _ := exec.CommandContext(ctx, e.config.NFTPath, "list", "chain", "ip", "agent_remote", "postrouting").CombinedOutput()
	if !strings.Contains(string(output), "10.231.0.0/16") {
		if err := runCommand(ctx, e.config.NFTPath, "add", "rule", "ip", "agent_remote", "postrouting", "ip", "saddr", "10.231.0.0/16", "masquerade"); err != nil {
			return err
		}
	}
	return e.ensureHostForwarding(ctx)
}

type nftForwardChain struct {
	Family string
	Table  string
	Name   string
}

func (e Engine) ensureHostForwarding(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, e.config.NFTPath, "--json", "list", "ruleset").CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect nftables forward chains: %w: %s", err, strings.TrimSpace(string(output)))
	}
	chains, err := parseNFTForwardChains(output)
	if err != nil {
		return err
	}
	for _, chain := range chains {
		chainOutput, listErr := exec.CommandContext(ctx, e.config.NFTPath, "list", "chain", chain.Family, chain.Table, chain.Name).CombinedOutput()
		if listErr != nil {
			return fmt.Errorf("inspect nftables chain %s/%s/%s: %w: %s", chain.Family, chain.Table, chain.Name, listErr, strings.TrimSpace(string(chainOutput)))
		}
		for _, rule := range []struct {
			comment string
			args    []string
		}{
			{comment: "agent-remote-native-egress", args: []string{"ip", "saddr", "10.231.0.0/16"}},
			{comment: "agent-remote-native-ingress", args: []string{"ip", "daddr", "10.231.0.0/16", "ct", "state", "established,related"}},
		} {
			if strings.Contains(string(chainOutput), rule.comment) {
				continue
			}
			args := []string{"insert", "rule", chain.Family, chain.Table, chain.Name}
			args = append(args, rule.args...)
			args = append(args, "accept", "comment", rule.comment)
			if err := runCommand(ctx, e.config.NFTPath, args...); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseNFTForwardChains(data []byte) ([]nftForwardChain, error) {
	var ruleset struct {
		NFTables []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(data, &ruleset); err != nil {
		return nil, fmt.Errorf("parse nftables ruleset: %w", err)
	}
	chains := make([]nftForwardChain, 0)
	for _, entry := range ruleset.NFTables {
		var value struct {
			Chain *struct {
				Family string `json:"family"`
				Table  string `json:"table"`
				Name   string `json:"name"`
				Hook   string `json:"hook"`
			} `json:"chain"`
		}
		if json.Unmarshal(entry, &value) == nil && value.Chain != nil && value.Chain.Hook == "forward" && (value.Chain.Family == "ip" || value.Chain.Family == "inet") {
			chains = append(chains, nftForwardChain{Family: value.Chain.Family, Table: value.Chain.Table, Name: value.Chain.Name})
		}
	}
	return chains, nil
}

func (e Engine) applyNamespaceFirewall(ctx context.Context, spec SessionSpec) error {
	namespace := spec.NetworkNamespace
	for _, args := range namespaceFirewallCommands(spec) {
		full := append([]string{"netns", "exec", namespace, e.config.NFTPath}, args...)
		if err := runCommand(ctx, e.config.IPPath, full...); err != nil {
			return err
		}
	}
	return nil
}

func namespaceFirewallCommands(spec SessionSpec) [][]string {
	commands := [][]string{
		{"add", "table", "inet", "agent_remote"},
		{"add", "chain", "inet", "agent_remote", "output", "{", "type", "filter", "hook", "output", "priority", "0", ";", "policy", "drop", ";", "}"},
		{"add", "rule", "inet", "agent_remote", "output", "ct", "state", "established,related", "accept"},
		{"add", "rule", "inet", "agent_remote", "output", "oifname", "lo", "accept"},
	}
	denied := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4"}
	// Explicit administrator allow rules must precede the baseline private-range rejects.
	// The policy has already been parsed and CIDR validated by the root helper.
	for _, cidr := range spec.Policy.NetworkAllowlist {
		commands = append(commands, []string{"add", "rule", "inet", "agent_remote", "output", "ip", "daddr", cidr, "accept"})
	}
	for _, cidr := range denied {
		commands = append(commands, []string{"add", "rule", "inet", "agent_remote", "output", "ip", "daddr", cidr, "reject"})
	}
	commands = append(commands,
		[]string{"add", "rule", "inet", "agent_remote", "output", "ip6", "daddr", "::/0", "reject"},
		[]string{"add", "rule", "inet", "agent_remote", "output", "ip", "daddr", "0.0.0.0/0", "accept"},
	)
	return commands
}

type runtimeIdentity struct {
	Username string
	UID      int
	GID      int
}

func (e Engine) identity(userID string) runtimeIdentity {
	identity, _ := e.lookupIdentity(userID)
	return identity
}

func (e Engine) ensureIdentity(userID string) (runtimeIdentity, error) {
	if identity, err := e.lookupIdentity(userID); err == nil {
		return identity, nil
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return runtimeIdentity{}, errors.New("creating runtime users requires root on Linux")
	}
	username := "ar-u-" + shortDigest(userID, 12)
	args := []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", username}
	if output, err := exec.Command("useradd", args...).CombinedOutput(); err != nil {
		return runtimeIdentity{}, fmt.Errorf("useradd failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return e.lookupIdentity(userID)
}

func (e Engine) lookupIdentity(userID string) (runtimeIdentity, error) {
	username := "ar-u-" + shortDigest(userID, 12)
	found, err := user.Lookup(username)
	if err != nil {
		return runtimeIdentity{}, err
	}
	uid, err := strconv.Atoi(found.Uid)
	if err != nil {
		return runtimeIdentity{}, err
	}
	gid, err := strconv.Atoi(found.Gid)
	if err != nil {
		return runtimeIdentity{}, err
	}
	return runtimeIdentity{Username: username, UID: uid, GID: gid}, nil
}

func (e Engine) prepareOwnedDirectories(userID string, paths ...string) error {
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if !pathInside(e.config.WorkspaceRoot, path) && !pathInside(e.config.AccountRoot, path) {
			return fmt.Errorf("runtime path is outside managed roots: %s", path)
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if runtime.GOOS == "linux" {
			if err := os.Chown(path, identity.UID, identity.GID); err != nil {
				return err
			}
			if err := e.applyDataACL(path, identity.Username); err != nil {
				return err
			}
			if err := e.grantManagedTraverse(path, identity.Username); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e Engine) installManagedClaudeSkills(userID string, accountPath string) error {
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return err
	}
	return managedskills.InstallClaude(accountPath, &managedskills.Ownership{UID: identity.UID, GID: identity.GID})
}

func (e Engine) preparePrivateDirectories(userID string, paths ...string) error {
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if !pathInside(e.config.WorkspaceRoot, path) && !pathInside(e.config.AccountRoot, path) {
			return fmt.Errorf("runtime path is outside managed roots: %s", path)
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if runtime.GOOS == "linux" {
			if err := os.Chown(path, identity.UID, identity.GID); err != nil {
				return err
			}
			if err := os.Chmod(path, 0o700); err != nil {
				return err
			}
			if err := e.grantManagedTraverse(path, identity.Username); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e Engine) prepareSyncedWorkspace(userID string, path string) error {
	if !pathInside(e.config.WorkspaceRoot, path) {
		return fmt.Errorf("workspace path is outside managed root: %s", path)
	}
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o770); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return nil
	}
	if err := e.clearDataACL(path); err != nil {
		return err
	}
	if err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := os.Lchown(current, identity.UID, identity.GID); err != nil {
			return err
		}
		return normalizeWorkspaceMode(current, entry)
	}); err != nil {
		return err
	}
	if err := e.applyDataACL(path, identity.Username); err != nil {
		return err
	}
	return e.grantManagedTraverse(path, identity.Username)
}

func (e Engine) ensureIndependentGitIndex(userID string, path string) error {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(gitPath, "index")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	identity, err := e.ensureIdentity(userID)
	if err != nil {
		return err
	}
	return initializeGitIndex(path, identity)
}

func initializeGitIndex(path string, identity runtimeIdentity) error {
	runGit := func(args ...string) error {
		base := []string{"-c", "core.fsmonitor=false", "-C", path}
		cmd := exec.Command("git", append(base, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if runtime.GOOS == "linux" && os.Geteuid() == 0 {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
				Uid: uint32(identity.UID), Gid: uint32(identity.GID), Groups: []uint32{uint32(identity.GID)},
			}}
		}
		return cmd.Run()
	}
	if err := runGit("rev-parse", "--verify", "--quiet", "HEAD^{tree}"); err == nil {
		if err := runGit("read-tree", "--reset", "HEAD"); err != nil {
			return fmt.Errorf("initialize workspace Git index from HEAD: %w", err)
		}
		return nil
	}
	if err := runGit("read-tree", "--empty"); err != nil {
		return fmt.Errorf("initialize empty workspace Git index: %w", err)
	}
	return nil
}

func (e Engine) grantManagedTraverse(path string, runtimeUser string) error {
	var managedRoot string
	for _, candidate := range []string{e.config.WorkspaceRoot, e.config.AccountRoot} {
		if pathInside(candidate, path) && len(candidate) > len(managedRoot) {
			managedRoot = filepath.Clean(candidate)
		}
	}
	if managedRoot == "" {
		return fmt.Errorf("runtime path is outside managed roots: %s", path)
	}
	aclRoot := filepath.Dir(managedRoot)
	targets := make([]string, 0)
	for _, parent := range parentDirectories(path) {
		if parent == aclRoot || pathInside(aclRoot, parent) {
			targets = append(targets, parent)
		}
	}
	access := namedUserACL(runtimeUser, e.config.NodeUser, "--x")
	args := append([]string{"-m", access}, targets...)
	if output, err := exec.Command(e.config.SetfaclPath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("grant managed path traverse access: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e Engine) applyDataACL(path string, runtimeUser string) error {
	access := namedUserACL(runtimeUser, e.config.NodeUser, "rwX")
	if output, err := exec.Command(e.config.SetfaclPath, "-R", "-m", access, path).CombinedOutput(); err != nil {
		return fmt.Errorf("setfacl access failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	defaultACL := namedDefaultUserACL(runtimeUser, e.config.NodeUser, "rwX")
	if output, err := exec.Command(e.config.SetfaclPath, "-m", defaultACL, path).CombinedOutput(); err != nil {
		return fmt.Errorf("setfacl default failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func namedUserACL(runtimeUser string, nodeUser string, permissions string) string {
	entries := []string{"u:" + runtimeUser + ":" + permissions}
	if nodeUser != runtimeUser {
		entries = append(entries, "u:"+nodeUser+":"+permissions)
	}
	return strings.Join(entries, ",")
}

func namedDefaultUserACL(runtimeUser string, nodeUser string, permissions string) string {
	entries := []string{"d:u:" + runtimeUser + ":" + permissions}
	if nodeUser != runtimeUser {
		entries = append(entries, "d:u:"+nodeUser+":"+permissions)
	}
	return strings.Join(entries, ",")
}

func (e Engine) applyDeviceControlACL(path string, runtimeUser string) error {
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	access := "u:" + runtimeUser + ":r-x,u:" + e.config.NodeUser + ":rwx"
	if runtimeUser == e.config.NodeUser {
		access = "u:" + runtimeUser + ":rwx"
	}
	if output, err := exec.Command(e.config.SetfaclPath, "-m", access, path).CombinedOutput(); err != nil {
		return fmt.Errorf("set device-control ACL failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e Engine) clearDataACL(path string) error {
	for _, args := range [][]string{{"-R", "-b", path}, {"-R", "-k", path}} {
		if output, err := exec.Command(e.config.SetfaclPath, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("clear workspace ACL failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func normalizeWorkspaceMode(path string, entry os.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return nil
	}
	if entry.IsDir() {
		return os.Chmod(path, 0o770)
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	mode := os.FileMode(0o660)
	if info.Mode().Perm()&0o100 != 0 {
		mode = 0o770
	}
	return os.Chmod(path, mode)
}

func (e Engine) specPath(sessionID string) string {
	return filepath.Join(e.config.StateRoot, "sessions", sessionID, "spec.json")
}

func (e Engine) saveSpec(spec SessionSpec) error {
	if spec.RuntimeConfig == nil {
		spec.RuntimeConfig = sessionRuntimeConfigFromEngine(e.config)
	}
	resolvers := make([]string, 0, len(e.config.DNSResolvers))
	for _, resolver := range e.config.DNSResolvers {
		resolvers = append(resolvers, "nameserver "+resolver)
	}
	resolvPath := filepath.Join(spec.SessionRoot, "resolv.conf")
	if err := os.WriteFile(resolvPath, []byte(strings.Join(resolvers, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(resolvPath, 0o644); err != nil {
		return err
	}
	timezonePath := filepath.Join(spec.SessionRoot, "timezone")
	if err := os.WriteFile(timezonePath, []byte(spec.Timezone+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Chmod(timezonePath, 0o644); err != nil {
		return err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	path := e.specPath(spec.SessionID)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func (e Engine) loadSpec(sessionID string) (SessionSpec, error) {
	if err := validateID(sessionID, "session_id"); err != nil {
		return SessionSpec{}, err
	}
	spec, err := readTrustedSpec(e.config, e.specPath(sessionID))
	if err != nil {
		return SessionSpec{}, err
	}
	return spec, nil
}

func (e Engine) cachedResult(requestID string) (map[string]any, bool, error) {
	path := filepath.Join(e.config.StateRoot, "tasks", shortDigest(requestID, 32)+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func (e Engine) saveResult(requestID string, result map[string]any) error {
	directory := filepath.Join(e.config.StateRoot, "tasks")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, shortDigest(requestID, 32)+".json"), append(data, '\n'), 0o600)
}

func shortDigest(value string, length int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	return encoded[:length]
}

func requiredText(payload map[string]any, key string) (string, error) {
	value, ok := payload[key].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func optionalText(payload map[string]any, key string, fallback string) string {
	value, _ := payload[key].(string)
	if value == "" {
		return fallback
	}
	return value
}

func textList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func claudeBindingArgv(payload map[string]any) ([]string, error) {
	template, ok := payload["template"].(map[string]any)
	if !ok {
		return nil, errors.New("template is required")
	}
	rawCommand, ok := template["command"].([]any)
	if !ok || len(rawCommand) == 0 {
		return nil, errors.New("template command is required")
	}
	command := make([]string, len(rawCommand))
	for index, value := range rawCommand {
		argument, ok := value.(string)
		if !ok || argument == "" || strings.ContainsRune(argument, '\x00') {
			return nil, errors.New("template command is invalid")
		}
		command[index] = argument
	}
	if command[0] != "claude" {
		return nil, errors.New("template command must use claude")
	}
	return command[1:], nil
}

func parseDeviceControlProtocol(value any, kind string) (int, error) {
	if value == nil {
		return 0, nil
	}
	configuration, ok := value.(map[string]any)
	if !ok || len(configuration) != 1 {
		return 0, errors.New("device_control is invalid")
	}
	version, ok := numericInt64(configuration["protocol_version"])
	if !ok || version != 1 || kind != "session" {
		return 0, errors.New("device_control protocol is unsupported")
	}
	return int(version), nil
}

func managedDeviceControlArgv(_ string, argv []string) ([]string, error) {
	return managedDeviceControlArgvForPaths(
		argv,
		"/opt/agent-remote/device/bin/agent-remote-device-proxy",
		"/run/agent-remote/device/context.json",
		"/run/agent-remote/device/bridge.sock",
	)
}

func managedDeviceControlArgvForPaths(argv []string, proxyCommand string, contextPath string, bridgeSocketPath string) ([]string, error) {
	for _, argument := range argv {
		if argument == "--mcp-config" || strings.HasPrefix(argument, "--mcp-config=") ||
			argument == "--strict-mcp-config" || strings.HasPrefix(argument, "--strict-mcp-config=") ||
			argument == "--settings" || strings.HasPrefix(argument, "--settings=") ||
			argument == "--setting-sources" || strings.HasPrefix(argument, "--setting-sources=") ||
			argument == "--safe-mode" || strings.HasPrefix(argument, "--safe-mode=") ||
			argument == "--bare" || strings.HasPrefix(argument, "--bare=") {
			return nil, errors.New("managed device control does not allow lifecycle or MCP configuration overrides")
		}
	}
	lifecycleSocket := "/tmp/lifecycle.sock"
	configuration := map[string]any{
		"mcpServers": map[string]any{
			"agent-remote-device": map[string]any{
				"type":    "stdio",
				"command": proxyCommand,
				"args": []string{
					"--managed-context", contextPath,
					"--bridge-socket", bridgeSocketPath,
					"--lifecycle-socket", lifecycleSocket,
					"--compact-tools",
					"--optimization-metrics", "/tmp/agent-remote-device-optimization.jsonl",
				},
			},
		},
	}
	encodedMCP, err := json.Marshal(configuration)
	if err != nil {
		return nil, err
	}
	hook := func(notification string) []map[string]any {
		return []map[string]any{{
			"hooks": []map[string]any{{
				"type": "command", "command": proxyCommand,
				"args":    []string{"--notify", notification, "--lifecycle-socket", lifecycleSocket},
				"timeout": 5,
			}},
		}}
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop":        hook("turn-stop"),
			"StopFailure": hook("turn-stop"),
			"SessionEnd":  hook("session-end"),
		},
	}
	encodedSettings, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	managed := []string{
		"--setting-sources", "user", "--settings", string(encodedSettings),
		"--strict-mcp-config", "--mcp-config", string(encodedMCP),
	}
	return append(managed, argv...), nil
}

func argumentPrefix(arguments []string, prefix []string) bool {
	if len(arguments) < len(prefix) {
		return false
	}
	for index := range prefix {
		if arguments[index] != prefix[index] {
			return false
		}
	}
	return true
}

func validateManagedDeviceProxy(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("managed device proxy path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("managed device proxy is unavailable or unsafe")
	}
	if runtime.GOOS == "linux" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return errors.New("managed device proxy is not root-owned")
		}
	}
	return nil
}

func parseRuntimePolicy(value any) (RuntimePolicy, error) {
	policy := defaultRuntimePolicy
	raw, ok := value.(map[string]any)
	if value == nil || !ok {
		if value != nil && !ok {
			return RuntimePolicy{}, errors.New("runtime_policy is invalid")
		}
		return policy, nil
	}
	limits := []struct {
		key     string
		target  *int64
		ceiling int64
		minimum int64
	}{
		{"memory_high_bytes", &policy.MemoryHighBytes, defaultRuntimePolicy.MemoryHighBytes, 64 << 20},
		{"memory_max_bytes", &policy.MemoryMaxBytes, defaultRuntimePolicy.MemoryMaxBytes, 64 << 20},
		{"cpu_quota_percent", &policy.CPUQuotaPercent, defaultRuntimePolicy.CPUQuotaPercent, 10},
		{"tasks_max", &policy.TasksMax, defaultRuntimePolicy.TasksMax, 16},
		{"limit_nofile", &policy.LimitNOFILE, defaultRuntimePolicy.LimitNOFILE, 256},
		{"tmpfs_size_bytes", &policy.TmpfsSizeBytes, defaultRuntimePolicy.TmpfsSizeBytes, 16 << 20},
	}
	for _, limit := range limits {
		rawValue, exists := raw[limit.key]
		if !exists {
			continue
		}
		number, valid := numericInt64(rawValue)
		if !valid || number < limit.minimum || number > limit.ceiling {
			return RuntimePolicy{}, fmt.Errorf("runtime_policy.%s is outside local limits", limit.key)
		}
		*limit.target = number
	}
	if policy.MemoryHighBytes > policy.MemoryMaxBytes {
		return RuntimePolicy{}, errors.New("runtime_policy.memory_high_bytes exceeds memory_max_bytes")
	}
	if allowlist, exists := raw["network_allowlist"]; exists {
		entries, ok := allowlist.([]any)
		if !ok || len(entries) > 64 {
			return RuntimePolicy{}, errors.New("runtime_policy.network_allowlist is invalid")
		}
		policy.NetworkAllowlist = make([]string, 0, len(entries))
		for _, entry := range entries {
			cidr, ok := entry.(string)
			if !ok {
				return RuntimePolicy{}, errors.New("runtime_policy.network_allowlist contains a non-string value")
			}
			ip, network, err := net.ParseCIDR(cidr)
			if err != nil || ip.To4() == nil {
				return RuntimePolicy{}, fmt.Errorf("runtime_policy.network_allowlist contains invalid IPv4 CIDR %q", cidr)
			}
			policy.NetworkAllowlist = append(policy.NetworkAllowlist, network.String())
		}
	}
	return policy, nil
}

func numericInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case float64:
		converted := int64(number)
		return converted, float64(converted) == number
	case int:
		return int64(number), true
	case int64:
		return number, true
	case json.Number:
		converted, err := number.Int64()
		return converted, err == nil
	default:
		return 0, false
	}
}

func validateID(value string, label string) error {
	if len(value) > 128 || value == "" {
		return fmt.Errorf("%s is invalid", label)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == ':' {
			continue
		}
		return fmt.Errorf("%s contains unsafe characters", label)
	}
	return nil
}

func validateName(value string, label string) error {
	return validateID(value, label)
}

func safeTimezone(value string) bool {
	if value == "UTC" {
		return true
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.Contains(value, "\\") {
		return false
	}
	zonePath := filepath.Join("/usr/share/zoneinfo", value)
	return pathInside("/usr/share/zoneinfo", zonePath) && pathExists(zonePath)
}

func safeLocale(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("_.@-", character) {
			continue
		}
		return false
	}
	return true
}

func pathInside(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(os.PathSeparator))
}

func ensureOwnedFile(path string, content []byte, mode os.FileMode, identity runtimeIdentity) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, content, mode); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return os.Chown(path, identity.UID, identity.GID)
	}
	return nil
}

func writeOwnedFile(path string, content []byte, mode os.FileMode, identity runtimeIdentity) error {
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return os.Chown(path, identity.UID, identity.GID)
	}
	return nil
}

func ensureRootDirectory(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, mode); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime state path is not a directory: %s", path)
	}
	if runtime.GOOS == "linux" {
		if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
			return fmt.Errorf("runtime state path is not root-owned: %s", path)
		}
	}
	return os.Chmod(path, mode)
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func commandSucceeds(name string, args ...string) bool {
	return exec.Command(name, args...).Run() == nil
}

func executableExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func kernelAtLeast(major int, minor int) bool {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	return dottedVersionAtLeast(strings.TrimSpace(string(data)), major, minor)
}

func systemdAtLeast(binary string, minimum int) bool {
	output, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 {
		return false
	}
	version, err := strconv.Atoi(fields[1])
	return err == nil && version >= minimum
}

func dottedVersionAtLeast(value string, major int, minor int) bool {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) < 2 {
		return false
	}
	foundMajor, majorErr := strconv.Atoi(parts[0])
	foundMinor, minorErr := strconv.Atoi(parts[1])
	return majorErr == nil && minorErr == nil && (foundMajor > major || (foundMajor == major && foundMinor >= minor))
}

func localeAvailable(locale string) bool {
	output, err := exec.Command("locale", "-a").Output()
	if err != nil {
		return false
	}
	wanted := normalizeLocale(locale)
	for _, available := range strings.Fields(string(output)) {
		if normalizeLocale(available) == wanted {
			return true
		}
	}
	return false
}

func normalizeLocale(value string) string {
	replacer := strings.NewReplacer("-", "", "_", "", ".", "")
	return strings.ToLower(replacer.Replace(value))
}

func runCommand(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ExecSpec replaces the current process with the validated Bubblewrap command.
func ExecSpec(config EngineConfig, specPath string) error {
	config = config.WithDefaults()
	spec, runtimeConfig, err := readRuntimeSpec(config, specPath)
	if err != nil {
		return err
	}
	if err := validateSpecEgoBrowserContext(runtimeConfig, &spec, true); err != nil {
		return err
	}
	args := bubblewrapArgs(runtimeConfig, spec)
	binary, err := exec.LookPath(runtimeConfig.BubblewrapPath)
	if err != nil {
		return err
	}
	return syscall.Exec(binary, append([]string{binary}, args...), clearEgoBrowserEnvironment(os.Environ()))
}

// SuperviseSpec starts tmux and keeps the systemd unit alive while the session exists.
func SuperviseSpec(config EngineConfig, specPath string) error {
	config = config.WithDefaults()
	spec, runtimeConfig, err := readRuntimeSpec(config, specPath)
	if err != nil {
		return err
	}
	if err := validateSpecEgoBrowserContext(runtimeConfig, &spec, true); err != nil {
		return err
	}
	commandParts := []string{shellQuote(runtimeConfig.RuntimeBinaryPath), "exec", "--state-root", shellQuote(runtimeConfig.StateRoot), "--spec", shellQuote(specPath)}
	command := strings.Join(commandParts, " ")
	cmd := exec.Command(runtimeConfig.TmuxBinaryPath, tmuxsession.NewSessionArgs(runtimeConfig.TmuxBinaryPath, spec.TmuxSocketPath, spec.TmuxSessionName, command)...)
	cmd.Dir = spec.WorkspacePath
	cmd.Env = withEgoBrowserEnvironment(replaceEnvironment(os.Environ(), "SHELL", "/bin/sh"), spec, false)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux start failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := tmuxsession.Configure(runtimeConfig.TmuxBinaryPath, spec.TmuxSocketPath, spec.TmuxSessionName); err != nil {
		return err
	}
	for {
		if exec.Command(runtimeConfig.TmuxBinaryPath, "-S", spec.TmuxSocketPath, "has-session", "-t", spec.TmuxSessionName).Run() != nil {
			if spec.BootID != "" {
				if err := os.WriteFile(processExitMarkerPath(spec), []byte(spec.BootID+"\n"), 0o600); err != nil {
					return fmt.Errorf("record runtime process exit: %w", err)
				}
			}
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

func replaceEnvironment(environ []string, key string, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

// AttachSession validates managed session state and attaches its tmux socket.
func AttachSession(config EngineConfig, sessionID string, runtimeBackend string, sshAgentSocket string) error {
	config = config.WithDefaults()
	if os.Geteuid() != 0 {
		return errors.New("managed session attach requires root")
	}
	if err := validateID(sessionID, "session_id"); err != nil {
		return err
	}
	tmuxSocketPath := ""
	tmuxSessionName := ""
	sshMode := ""
	sshAgentDirectory := ""
	uid, gid := 0, 0
	dropPrivileges := false
	switch runtimeBackend {
	case "native":
		spec, err := readTrustedSpec(config, filepath.Join(config.StateRoot, "sessions", sessionID, "spec.json"))
		if err != nil {
			return err
		}
		found, err := user.Lookup(spec.Username)
		if err != nil {
			return err
		}
		uid, err = strconv.Atoi(found.Uid)
		if err != nil {
			return err
		}
		gid, err = strconv.Atoi(found.Gid)
		if err != nil {
			return err
		}
		tmuxSocketPath = spec.TmuxSocketPath
		tmuxSessionName = spec.TmuxSessionName
		sshMode = spec.SSHMode
		sshAgentDirectory = spec.SSHAgentDirectory
		dropPrivileges = true
	case "docker_sandbox":
		engine := NewEngine(config)
		spec, err := engine.loadDockerSessionSpec(sessionID)
		if err != nil {
			return err
		}
		uid, gid = spec.RuntimeUID, spec.RuntimeGID
		tmuxSessionName = spec.TmuxSessionName
		sshMode = spec.SSHMode
		sshAgentDirectory = spec.SSHAgentDirectory
	default:
		return errors.New("managed session attach backend is unsupported")
	}
	var proxy *sshAgentProxy
	if sshAgentSocket != "" {
		if sshMode != "agent_forwarding" || sshAgentDirectory == "" {
			return errors.New("SSH agent forwarding is not enabled for this session")
		}
		if err := validateForwardedSSHAgentSocket(sshAgentSocket); err != nil {
			return err
		}
		startedProxy, err := startSSHAgentProxy(sshAgentDirectory, sshAgentSocket, uid, gid)
		if err != nil {
			return err
		}
		proxy = startedProxy
		defer proxy.Close()
	}
	binary, err := exec.LookPath(config.TmuxBinaryPath)
	if err != nil {
		return err
	}
	if err := tmuxsession.Configure(binary, tmuxSocketPath, tmuxSessionName); err != nil {
		return err
	}
	cmd := exec.Command(binary, tmuxsession.AttachArgs(tmuxSocketPath, tmuxSessionName)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if dropPrivileges {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
			Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)},
		}}
	}
	return cmd.Run()
}

type sshAgentProxy struct {
	listener   *net.UnixListener
	directory  string
	socketPath string
	stablePath string
	upstream   string
}

func startSSHAgentProxy(directory string, upstream string, uid int, gid int) (*sshAgentProxy, error) {
	socketPath := filepath.Join(directory, fmt.Sprintf("a.%d", os.Getpid()))
	stablePath := filepath.Join(directory, "agent.sock")
	_ = os.Remove(socketPath)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	proxy := &sshAgentProxy{
		listener: listener, directory: directory, socketPath: socketPath,
		stablePath: stablePath, upstream: upstream,
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		proxy.Close()
		return nil, err
	}
	if runtime.GOOS == "linux" {
		if err := os.Chown(socketPath, uid, gid); err != nil {
			proxy.Close()
			return nil, err
		}
	}
	temporaryLink := filepath.Join(directory, fmt.Sprintf(".l.%d", os.Getpid()))
	_ = os.Remove(temporaryLink)
	if err := os.Symlink(filepath.Base(socketPath), temporaryLink); err != nil {
		proxy.Close()
		return nil, err
	}
	if err := os.Rename(temporaryLink, stablePath); err != nil {
		_ = os.Remove(temporaryLink)
		proxy.Close()
		return nil, err
	}
	go proxy.serve()
	return proxy, nil
}

func (p *sshAgentProxy) serve() {
	for {
		client, err := p.listener.AcceptUnix()
		if err != nil {
			return
		}
		go p.forward(client)
	}
}

func (p *sshAgentProxy) forward(client *net.UnixConn) {
	defer client.Close()
	upstream, err := net.Dial("unix", p.upstream)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
}

// Close stops the proxy listener and waits for active forwarding loops to exit.
func (p *sshAgentProxy) Close() error {
	err := p.listener.Close()
	if target, readErr := os.Readlink(p.stablePath); readErr == nil && target == filepath.Base(p.socketPath) {
		_ = os.Remove(p.stablePath)
	}
	_ = os.Remove(p.socketPath)
	return err
}

func validateForwardedSSHAgentSocket(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 {
		return errors.New("forwarded SSH agent socket path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect forwarded SSH agent socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("forwarded SSH agent path is not a Unix socket")
	}
	if runtime.GOOS == "linux" {
		sudoUID, parseErr := strconv.Atoi(os.Getenv("SUDO_UID"))
		stat, ok := info.Sys().(*syscall.Stat_t)
		if parseErr != nil || !ok || int(stat.Uid) != sudoUID {
			return errors.New("forwarded SSH agent socket owner does not match the SSH gateway user")
		}
	}
	return nil
}

// SyncCommand executes an SSH transport command inside the verified user's filesystem sandbox.
func SyncCommand(config EngineConfig, userID string, command string) error {
	config = config.WithDefaults()
	if os.Geteuid() != 0 {
		return errors.New("sync command requires root")
	}
	if err := validateID(userID, "user_id"); err != nil {
		return err
	}
	if command == "" || len(command) > 4096 || strings.ContainsAny(command, "\x00\r\n") {
		return errors.New("sync command is invalid")
	}
	engine := NewEngine(config)
	identity, err := engine.ensureIdentity(userID)
	if err != nil {
		return err
	}
	userRoot := filepath.Join(config.WorkspaceRoot, userID)
	syncHome := filepath.Join(userRoot, ".sync-home")
	syncTemp := filepath.Join(userRoot, ".sync-tmp")
	if err := engine.preparePrivateDirectories(userID, userRoot, syncHome, syncTemp); err != nil {
		return err
	}
	args := []string{
		"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid",
		"--unshare-ipc", "--unshare-uts", "--unshare-net", "--proc", "/proc",
		"--dev", "/dev", "--dir", "/tmp", "--dir", "/etc", "--dir", "/home", "--dir", "/home/runtime",
	}
	for _, path := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if pathExists(path) {
			args = append(args, "--ro-bind", path, path)
		}
	}
	for _, path := range []string{"/etc/passwd", "/etc/group", "/etc/nsswitch.conf"} {
		if pathExists(path) {
			args = append(args, "--ro-bind", path, path)
		}
	}
	for _, path := range parentDirectories(userRoot) {
		args = append(args, "--dir", path)
	}
	args = append(args,
		"--bind", userRoot, userRoot,
		"--setenv", "HOME", syncHome,
		"--setenv", "TMPDIR", syncTemp,
		"--chdir", syncHome,
		"--", "/bin/sh", "-lc", command,
	)
	if err := syscall.Setgroups([]int{identity.GID}); err != nil {
		return err
	}
	if err := syscall.Setgid(identity.GID); err != nil {
		return err
	}
	if err := syscall.Setuid(identity.UID); err != nil {
		return err
	}
	binary, err := exec.LookPath(config.BubblewrapPath)
	if err != nil {
		return err
	}
	return syscall.Exec(binary, append([]string{binary}, args...), os.Environ())
}

func parentDirectories(path string) []string {
	cleaned := filepath.Clean(path)
	parts := strings.Split(strings.TrimPrefix(cleaned, string(os.PathSeparator)), string(os.PathSeparator))
	current := ""
	result := make([]string, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		current += string(os.PathSeparator) + part
		result = append(result, current)
	}
	return result
}

func readTrustedSpec(config EngineConfig, specPath string) (SessionSpec, error) {
	spec, _, err := readRuntimeSpec(config, specPath)
	if err != nil {
		return SessionSpec{}, err
	}
	return spec, nil
}

func bubblewrapArgs(config EngineConfig, spec SessionSpec) []string {
	args := []string{"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--proc", "/proc", "--dev", "/dev", "--dir", "/etc", "--dir", "/workspace", "--dir", "/account", "--dir", "/home", "--dir", "/home/runtime", "--dir", "/run", "--dir", "/run/agent-remote"}
	for _, path := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if pathExists(path) {
			args = append(args, "--ro-bind", path, path)
		}
	}
	args = append(args,
		"--ro-bind", filepath.Join(spec.SessionRoot, "passwd"), "/etc/passwd",
		"--ro-bind", filepath.Join(spec.SessionRoot, "group"), "/etc/group",
	)
	if pathExists("/etc/nsswitch.conf") {
		args = append(args, "--ro-bind", "/etc/nsswitch.conf", "/etc/nsswitch.conf")
	}
	args = append(args,
		"--ro-bind", spec.RuntimeRoot, "/opt/agent-remote/runtime",
		"--bind", spec.WorkspacePath, "/workspace",
		"--bind", spec.AccountPath, "/account",
		"--bind", filepath.Join(spec.SessionRoot, "tmp"), "/tmp",
		"--bind", filepath.Join(spec.AccountPath, ".claude"), "/home/runtime/.claude",
		"--bind", filepath.Join(spec.AccountPath, ".claude.json"), "/home/runtime/.claude.json",
		"--bind", filepath.Join(spec.AccountPath, ".claude.json"), "/home/runtime/.claude/.claude.json",
		"--ro-bind", filepath.Join("/usr/share/zoneinfo", spec.Timezone), "/etc/localtime",
		"--ro-bind", filepath.Join(spec.SessionRoot, "timezone"), "/etc/timezone",
		"--ro-bind", filepath.Join(spec.SessionRoot, "resolv.conf"), "/etc/resolv.conf",
		"--setenv", "HOME", "/home/runtime",
		"--setenv", "TMPDIR", "/tmp",
		"--setenv", "XDG_CACHE_HOME", "/tmp/xdg-cache",
		"--setenv", "CLAUDE_CONFIG_DIR", "/home/runtime/.claude",
		"--setenv", "PATH", "/opt/agent-remote/runtime/bin:/opt/agent-remote/ego-browser/bin:/usr/local/bin:/usr/bin:/bin",
		"--setenv", "TZ", spec.Timezone,
		"--setenv", "LANG", spec.Locale,
		"--setenv", "LC_ALL", spec.Locale,
		"--setenv", "LANGUAGE", spec.Locale,
	)
	if spec.EgoBrowserEnabled {
		// Keep the wrapper and broker endpoint at stable in-sandbox paths. The
		// host-side nonce remains supplied only through the supervisor environment
		// and is copied into the sandbox environment below.
		args = append(args,
			"--dir", "/opt", "--dir", "/opt/agent-remote", "--dir", "/opt/agent-remote/ego-browser",
			"--dir", "/opt/agent-remote/ego-browser/bin",
			"--ro-bind", spec.EgoBrowserWrapperPath, egoBrowserSandboxWrapperPath,
			"--ro-bind", spec.EgoBrowserSkillPath, egoBrowserSandboxSkillPath,
			"--dir", egoBrowserSandboxSocketRoot,
			"--bind", filepath.Dir(spec.EgoBrowserBrokerSocket), egoBrowserSandboxSocketRoot,
		)
		for _, entry := range egoBrowserEnvironment(spec, true) {
			key, value, ok := strings.Cut(entry, "=")
			if ok {
				args = append(args, "--setenv", key, value)
			}
		}
	}
	if spec.DeveloperCredentialProfilePath != "" {
		args = append(args,
			"--dir", "/developer-profile", "--dir", "/home/runtime/.ssh",
			"--bind", spec.DeveloperCredentialProfilePath, "/developer-profile",
			"--bind", filepath.Join(spec.DeveloperCredentialProfilePath, ".ssh"), "/home/runtime/.ssh",
			"--setenv", "GIT_CONFIG_GLOBAL", "/developer-profile/home/.gitconfig",
			"--setenv", "GH_CONFIG_DIR", "/developer-profile/gh",
			"--setenv", "AGENT_REMOTE_DEVELOPER_CREDENTIAL_PROFILE_PATH", "/developer-profile",
		)
	}
	if spec.SSHAgentDirectory != "" {
		args = append(args,
			"--dir", "/run/agent-remote/ssh-agent",
			"--bind", spec.SSHAgentDirectory, "/run/agent-remote/ssh-agent",
			"--setenv", "SSH_AUTH_SOCK", "/run/agent-remote/ssh-agent/agent.sock",
		)
	}
	if spec.DeviceControlProtocolVersion == 1 {
		args = append(args,
			"--ro-bind", spec.DeviceControlDirectory, "/run/agent-remote/device",
			"--ro-bind", spec.DeviceProxyPath,
			"/opt/agent-remote/device/bin/agent-remote-device-proxy",
		)
	}
	for _, path := range []string{"/etc/ssl", "/etc/pki"} {
		if pathExists(path) {
			args = append(args, "--ro-bind", path, path)
		}
	}
	args = append(args, "--chdir", "/workspace", "--", spec.RuntimeCommand)
	return append(args, spec.Argv...)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
