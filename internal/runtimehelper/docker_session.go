package runtimehelper

import (
	"bytes"
	"context"
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
	"strconv"
	"strings"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
	"golang.org/x/sys/unix"
)

const (
	dockerSessionSpecVersion = 1
	dockerSessionKindBinding = "binding"
	dockerSessionKindTool    = "session"
)

// DockerSessionSpec is the root-owned identity and feature manifest for one
// tmux-held Docker Sandbox session.
type DockerSessionSpec struct {
	Version                      int    `json:"version,omitempty"`
	Kind                         string `json:"kind,omitempty"`
	SessionID                    string `json:"session_id"`
	UserID                       string `json:"user_id,omitempty"`
	TmuxSessionName              string `json:"tmux_session_name"`
	SandboxName                  string `json:"sandbox_name"`
	BootID                       string `json:"boot_id,omitempty"`
	RuntimeUID                   int    `json:"runtime_uid,omitempty"`
	RuntimeGID                   int    `json:"runtime_gid,omitempty"`
	SSHMode                      string `json:"ssh_mode,omitempty"`
	SSHAgentDirectory            string `json:"ssh_agent_directory,omitempty"`
	DeviceControlProtocolVersion int    `json:"device_control_protocol_version,omitempty"`
	DeviceControlDirectory       string `json:"device_control_directory,omitempty"`
	DeviceProxyPath              string `json:"device_proxy_path,omitempty"`
}

func (e Engine) dockerRuntimeIdentity() (runtimeIdentity, error) {
	found, err := user.Lookup(e.config.WithDefaults().NodeUser)
	if err != nil {
		return runtimeIdentity{}, err
	}
	uid, uidErr := strconv.Atoi(found.Uid)
	gid, gidErr := strconv.Atoi(found.Gid)
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
		return runtimeIdentity{}, errors.New("Docker runtime identity is invalid")
	}
	return runtimeIdentity{Username: found.Username, UID: uid, GID: gid}, nil
}

func (e Engine) dockerSessionRoot(sessionID string) string {
	return filepath.Join(e.config.WithDefaults().StateRoot, "docker-sessions", sessionID)
}

func (e Engine) dockerSessionSpecPath(sessionID string) string {
	return filepath.Join(e.dockerSessionRoot(sessionID), "spec.json")
}

func (e Engine) legacyDockerSessionSpecPath(sessionID string) string {
	return filepath.Join(e.config.WithDefaults().StateRoot, "docker-sessions", sessionID+".json")
}

func (e Engine) saveDockerSessionSpec(spec DockerSessionSpec) error {
	e.config = e.config.WithDefaults()
	spec.Version = dockerSessionSpecVersion
	if err := e.validateDockerSessionSpec(spec, spec.SessionID); err != nil {
		return err
	}
	root := e.dockerSessionRoot(spec.SessionID)
	if err := ensureRootDirectory(root, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	path := e.dockerSessionSpecPath(spec.SessionID)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return e.grantDockerSessionStateTraversal(spec)
}

func (e Engine) loadDockerSessionSpec(sessionID string) (DockerSessionSpec, error) {
	if err := validateID(sessionID, "session_id"); err != nil {
		return DockerSessionSpec{}, err
	}
	path := e.dockerSessionSpecPath(sessionID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		path = e.legacyDockerSessionSpecPath(sessionID)
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return DockerSessionSpec{}, err
	}
	if err := validateRootOwnedStateFile(path, int64(len(data))); err != nil {
		return DockerSessionSpec{}, err
	}
	var spec DockerSessionSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return DockerSessionSpec{}, err
	}
	if err := e.validateDockerSessionSpec(spec, sessionID); err != nil {
		return DockerSessionSpec{}, err
	}
	return spec, nil
}

func (e Engine) validateDockerSessionSpec(spec DockerSessionSpec, sessionID string) error {
	if spec.SessionID != sessionID || validateID(spec.SessionID, "session_id") != nil ||
		validateName(spec.TmuxSessionName, "tmux_session_name") != nil ||
		validateName(spec.SandboxName, "sandbox_name") != nil {
		return errors.New("Docker session spec identity is invalid")
	}
	if spec.Version == 0 {
		return nil
	}
	if spec.Version != dockerSessionSpecVersion ||
		(spec.Kind != dockerSessionKindBinding && spec.Kind != dockerSessionKindTool) ||
		validateID(spec.UserID, "user_id") != nil ||
		spec.RuntimeUID <= 0 || spec.RuntimeGID <= 0 {
		return errors.New("Docker session spec version or runtime identity is invalid")
	}
	if spec.Kind == dockerSessionKindBinding &&
		(spec.SSHMode != "" || spec.SSHAgentDirectory != "" || spec.DeviceControlProtocolVersion != 0 ||
			spec.DeviceControlDirectory != "" || spec.DeviceProxyPath != "") {
		return errors.New("Docker binding spec contains tool-session features")
	}
	if spec.SSHMode != "" && spec.SSHMode != "disabled" && spec.SSHMode != "deploy_key" && spec.SSHMode != "agent_forwarding" {
		return errors.New("Docker session spec contains an invalid SSH mode")
	}
	if spec.SSHMode == "agent_forwarding" {
		if spec.SSHAgentDirectory != filepath.Join(e.dockerSessionRoot(sessionID), "ssh-agent") {
			return errors.New("Docker session spec contains an invalid SSH agent directory")
		}
	} else if spec.SSHAgentDirectory != "" {
		return errors.New("Docker session spec contains an unconfigured SSH agent directory")
	}
	if spec.DeviceControlProtocolVersion == 1 {
		if spec.DeviceControlDirectory != filepath.Join(e.dockerSessionRoot(sessionID), "device-control") ||
			filepath.Clean(spec.DeviceProxyPath) != filepath.Clean(e.config.WithDefaults().DeviceProxyPath) {
			return errors.New("Docker session spec contains invalid device control paths")
		}
	} else if spec.DeviceControlProtocolVersion != 0 || spec.DeviceControlDirectory != "" || spec.DeviceProxyPath != "" {
		return errors.New("Docker session spec contains unconfigured device control paths")
	}
	return nil
}

func (e Engine) grantDockerSessionStateTraversal(spec DockerSessionSpec) error {
	if spec.DeviceControlProtocolVersion != 1 {
		return nil
	}
	stateRoot := e.config.WithDefaults().StateRoot
	paths := []string{
		stateRoot,
		filepath.Join(stateRoot, "docker-sessions"),
		e.dockerSessionRoot(spec.SessionID),
	}
	entry := "u:" + strconv.Itoa(spec.RuntimeUID) + ":--x"
	for _, path := range paths {
		if output, err := exec.Command(e.config.WithDefaults().SetfaclPath, "-m", entry, path).CombinedOutput(); err != nil {
			return fmt.Errorf("grant Docker session state traversal: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func (e Engine) prepareDockerSessionRuntime(
	payload map[string]any,
	decoded toolsessions.CreatePayload,
	egoContext egoBrowserRuntimeContext,
) (DockerSessionSpec, toolsessions.SandboxRuntime, error) {
	identity, err := e.dockerRuntimeIdentity()
	if err != nil {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	spec := DockerSessionSpec{
		Version: dockerSessionSpecVersion, Kind: dockerSessionKindTool,
		SessionID: decoded.SessionID, UserID: decoded.UserID,
		TmuxSessionName: decoded.TmuxSessionName, SandboxName: decoded.SandboxName,
		BootID: currentBootID(), RuntimeUID: identity.UID, RuntimeGID: identity.GID,
	}
	runtimeConfig := toolsessions.SandboxRuntime{
		UID: identity.UID, GID: identity.GID, SetfaclPath: e.config.WithDefaults().SetfaclPath,
	}
	if decoded.DeveloperCredentials != nil {
		spec.SSHMode = decoded.DeveloperCredentials.SSHMode
		if spec.SSHMode != "disabled" && spec.SSHMode != "deploy_key" && spec.SSHMode != "agent_forwarding" {
			return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, errors.New("developer_credentials.ssh_mode is invalid")
		}
		if spec.SSHMode == "agent_forwarding" {
			spec.SSHAgentDirectory = filepath.Join(e.dockerSessionRoot(decoded.SessionID), "ssh-agent")
			if err := ensureRootDirectory(spec.SSHAgentDirectory, 0o711); err != nil {
				return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
			}
			runtimeConfig.Mounts = append(runtimeConfig.Mounts, spec.SSHAgentDirectory)
			runtimeConfig.Environment = append(runtimeConfig.Environment,
				"SSH_AUTH_SOCK="+filepath.Join(spec.SSHAgentDirectory, "agent.sock"),
			)
		}
	}
	if egoContext.Enabled {
		if err := validateEgoBrowserArtifacts(
			egoContext.WrapperPath,
			egoContext.WrapperVersion,
			egoContext.SkillPath,
			egoContext.SkillVersion,
			egoContext.SkillTreeSHA256,
		); err != nil {
			return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
		}
		runtimeConfig.Mounts = append(runtimeConfig.Mounts,
			filepath.Dir(egoContext.WrapperPath),
			egoContext.SkillPath,
			filepath.Dir(egoContext.BrokerSocket),
		)
		runtimeConfig.Environment = append(runtimeConfig.Environment,
			"PATH="+filepath.Dir(egoContext.WrapperPath)+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"EGO_BROWSER_ENABLED=1",
			"EGO_BROWSER_WRAPPER_PATH="+egoContext.WrapperPath,
			"EGO_BROWSER_BROKER_SOCKET="+egoContext.BrokerSocket,
			"EGO_BROWSER_BROKER_NONCE="+egoContext.BrokerNonce,
			"EGO_BROWSER_PROTOCOL_VERSION="+egoContext.Protocol,
			"EGO_BROWSER_WRAPPER_VERSION="+egoContext.WrapperVersion,
			"EGO_BROWSER_DEFAULT_TASK_SPACE="+egoContext.TaskSpace,
		)
	}
	deviceProtocol, err := parseDeviceControlProtocol(payload["device_control"], "session")
	if err != nil {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	if deviceProtocol == 0 {
		return spec, runtimeConfig, nil
	}
	if err := validateManagedDeviceProxy(e.config.WithDefaults().DeviceProxyPath); err != nil {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	stateRoot := e.dockerSessionRoot(decoded.SessionID)
	if err := ensureRootDirectory(stateRoot, 0o700); err != nil {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	deviceDirectory := filepath.Join(stateRoot, "device-control")
	if err := os.Mkdir(deviceDirectory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	if err := e.applyDeviceControlACL(deviceDirectory, identity.Username); err != nil {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	command := decoded.Template.Command
	if len(command) == 0 {
		command = append([]string{decoded.ToolType}, decoded.Argv...)
	}
	if len(command) == 0 || command[0] != "claude" {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, errors.New("managed device control requires the Claude command")
	}
	arguments, err := managedDeviceControlArgvForPaths(
		command[1:],
		e.config.WithDefaults().DeviceProxyPath,
		filepath.Join(deviceDirectory, "context.json"),
		filepath.Join(deviceDirectory, "bridge.sock"),
	)
	if err != nil {
		return DockerSessionSpec{}, toolsessions.SandboxRuntime{}, err
	}
	runtimeConfig.ManagedArguments = arguments[:len(arguments)-len(command[1:])]
	runtimeConfig.Mounts = append(runtimeConfig.Mounts,
		deviceDirectory,
		filepath.Dir(e.config.WithDefaults().DeviceProxyPath),
	)
	spec.DeviceControlProtocolVersion = deviceProtocol
	spec.DeviceControlDirectory = deviceDirectory
	spec.DeviceProxyPath = filepath.Clean(e.config.WithDefaults().DeviceProxyPath)
	return spec, runtimeConfig, nil
}

func (e Engine) prepareDockerBindingRuntime() (runtimeIdentity, toolaccounts.SandboxRuntime, error) {
	identity, err := e.dockerRuntimeIdentity()
	if err != nil {
		return runtimeIdentity{}, toolaccounts.SandboxRuntime{}, err
	}
	return identity, toolaccounts.SandboxRuntime{
		UID: identity.UID, GID: identity.GID, SetfaclPath: e.config.WithDefaults().SetfaclPath,
	}, nil
}

func validateRootOwnedStateFile(path string, size int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 ||
		size > 64*1024 {
		return errors.New("Docker session spec file is unsafe")
	}
	if runtime.GOOS == "linux" {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return errors.New("Docker session spec is not root-owned")
		}
	}
	return nil
}

func dockerLoopbackProxyScript() string {
	return strings.Join([]string{
		"const net=require('node:net')",
		"const port=Number(process.argv[1])",
		"if(!Number.isInteger(port)||port<1||port>65535)process.exit(64)",
		"const socket=net.connect({host:'127.0.0.1',port})",
		"socket.on('error',()=>process.exit(69))",
		"process.stdin.pipe(socket)",
		"socket.pipe(process.stdout)",
	}, ";")
}

func (e Engine) dialDockerSessionLoopback(ctx context.Context, spec DockerSessionSpec, port int) (net.Conn, error) {
	if spec.Version != dockerSessionSpecVersion || dockerSessionKind(spec) != dockerSessionKindTool || spec.RuntimeUID <= 0 || spec.RuntimeGID <= 0 {
		return nil, errors.New("managed Docker session runtime identity is unavailable")
	}
	command := dockerLoopbackProxyCommand(e.config.WithDefaults(), spec, port)
	return startStdioProxy(ctx, command[0], command[1:]...)
}

func dockerSessionKind(spec DockerSessionSpec) string {
	if spec.Version == 0 || spec.Kind == "" {
		return dockerSessionKindTool
	}
	return spec.Kind
}

func dockerLoopbackProxyCommand(config EngineConfig, spec DockerSessionSpec, port int) []string {
	return []string{
		config.DockerBinaryPath,
		"sandbox", "exec", "-i",
		"-u", strconv.Itoa(spec.RuntimeUID) + ":" + strconv.Itoa(spec.RuntimeGID),
		spec.SandboxName,
		"node", "-e", dockerLoopbackProxyScript(), strconv.Itoa(port),
	}
}

func startStdioProxy(ctx context.Context, executable string, arguments ...string) (net.Conn, error) {
	fileDescriptors, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("create Docker loopback socket pair: %w", err)
	}
	unix.CloseOnExec(fileDescriptors[0])
	unix.CloseOnExec(fileDescriptors[1])
	parent := os.NewFile(uintptr(fileDescriptors[0]), "docker-loopback-parent")
	child := os.NewFile(uintptr(fileDescriptors[1]), "docker-loopback-child")
	if parent == nil || child == nil {
		if parent != nil {
			_ = parent.Close()
		}
		if child != nil {
			_ = child.Close()
		}
		return nil, errors.New("create Docker loopback socket files")
	}

	command := exec.CommandContext(ctx, executable, arguments...)
	command.Stdin = child
	command.Stdout = child
	command.Stderr = io.Discard
	command.Env = clearEgoBrowserEnvironment(os.Environ())
	if err := command.Start(); err != nil {
		_ = parent.Close()
		_ = child.Close()
		return nil, fmt.Errorf("start Docker loopback proxy: %w", err)
	}
	_ = child.Close()
	connection, err := net.FileConn(parent)
	_ = parent.Close()
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("adopt Docker loopback proxy: %w", err)
	}
	go func() { _ = command.Wait() }()
	return connection, nil
}

func (e Engine) removeDockerSessionSpec(sessionID string) error {
	if err := validateID(sessionID, "session_id"); err != nil {
		return err
	}
	for _, path := range []string{e.legacyDockerSessionSpecPath(sessionID), e.dockerSessionRoot(sessionID)} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func dockerSessionProcessExited(spec DockerSessionSpec, active bool, bootID string) bool {
	return !active && bootID != "" && spec.BootID == bootID
}
