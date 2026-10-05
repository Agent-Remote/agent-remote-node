package runtimehelper

// The Docker capability is deliberately a small broker instead of a mounted
// docker.sock. Claude receives a session-owned Unix socket and an immutable
// wrapper. The broker authenticates the peer UID, validates the command, and
// adds a session label before invoking the host Docker CLI.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dockerCapabilityVersion   = 1
	dockerCapabilityMaxLine   = 64 << 10
	dockerCapabilityMaxOutput = 8 << 20
)

const dockerCapabilityWrapperTemplate = `#!/bin/sh
set -eu
socket=${FCLAUDE_DOCKER_SOCKET:-/run/agent-remote/docker/broker.sock}
exec node - "$socket" "$@" <<'NODE'
const net = require('node:net');
const socketPath = process.argv[2];
const args = process.argv.slice(3);
if (!socketPath || args.length === 0) process.exit(64);
const socket = net.createConnection(socketPath);
let data = '';
socket.on('connect', () => socket.end(JSON.stringify({version: 1, session_id: '__SESSION_ID__', args}) + '\n'));
socket.on('data', chunk => data += chunk);
socket.on('end', () => {
  try {
    const response = JSON.parse(data);
    if (response.stdout) process.stdout.write(Buffer.from(response.stdout, 'base64'));
    if (response.stderr) process.stderr.write(Buffer.from(response.stderr, 'base64'));
    process.exit(Number.isInteger(response.exit_code) ? response.exit_code : 1);
  } catch (_) { process.exit(69); }
});
socket.on('error', () => process.exit(69));
NODE
`

type dockerCapabilityRequest struct {
	Version   int      `json:"version"`
	SessionID string   `json:"session_id"`
	Args      []string `json:"args"`
}

type dockerCapabilityResponse struct {
	Version  int    `json:"version"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

type dockerCapabilityBroker struct {
	listener *net.UnixListener
	socket   string
	uid      int
	session  string
	workdir  string
	docker   string
	done     chan struct{}
	close    sync.Once
	mu       sync.Mutex
}

var dockerCapabilityBrokers sync.Map

func startDockerCapability(socketPath, sessionID, workdir, docker string, uid, gid int) (*dockerCapabilityBroker, error) {
	if err := validateID(sessionID, "session_id"); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath || !filepath.IsAbs(workdir) {
		return nil, errors.New("Docker capability path is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(socketPath)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		listener.Close()
		return nil, err
	}
	if os.Geteuid() == 0 && uid > 0 && gid > 0 {
		_ = os.Chown(socketPath, uid, gid)
	}
	broker := &dockerCapabilityBroker{listener: listener, socket: socketPath, uid: uid, session: sessionID, workdir: workdir, docker: docker, done: make(chan struct{})}
	dockerCapabilityBrokers.Store(socketPath, broker)
	go broker.serve()
	return broker, nil
}

func (b *dockerCapabilityBroker) serve() {
	for {
		connection, err := b.listener.AcceptUnix()
		if err != nil {
			select {
			case <-b.done:
				return
			default:
				return
			}
		}
		go b.handle(connection)
	}
}

func (b *dockerCapabilityBroker) handle(connection *net.UnixConn) {
	defer connection.Close()
	if b.uid > 0 {
		uid, err := peerUID(connection)
		if err != nil || uid != b.uid {
			return
		}
	}
	reader := bufio.NewReader(connection)
	line, err := reader.ReadBytes('\n')
	if err != nil || len(line) > dockerCapabilityMaxLine {
		return
	}
	if err := rejectDuplicateJSONKeys(line); err != nil {
		return
	}
	var request dockerCapabilityRequest
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Version != dockerCapabilityVersion || request.SessionID != b.session {
		return
	}
	response := b.execute(request.Args)
	_ = json.NewEncoder(connection).Encode(response)
}

func (b *dockerCapabilityBroker) execute(args []string) dockerCapabilityResponse {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := validateDockerCapabilityArgs(args, b.session, b.workdir); err != nil {
		return dockerCapabilityResponse{Version: dockerCapabilityVersion, ExitCode: 125, Stderr: base64.StdEncoding.EncodeToString([]byte(err.Error()))}
	}
	args = scopeDockerCapabilityArgs(args, b.session)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	go func() {
		select {
		case <-b.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	if target := dockerCapabilityTarget(args); target != "" {
		check := exec.CommandContext(ctx, b.docker, "inspect", "--format={{index .Config.Labels \"com.agent-remote.session\"}}", target)
		check.Dir = b.workdir
		output, err := check.Output()
		if err != nil || strings.TrimSpace(string(output)) != b.session {
			return dockerCapabilityResponse{Version: dockerCapabilityVersion, ExitCode: 1, Stderr: base64.StdEncoding.EncodeToString([]byte("docker resource is outside this session\n"))}
		}
	}
	command := exec.CommandContext(ctx, b.docker, args...)
	command.Dir = b.workdir
	stdout, stderr := boundedBuffer{}, boundedBuffer{}
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		if ctx.Err() != nil {
			exitCode = 124
		} else if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 127
		}
	}
	return dockerCapabilityResponse{Version: dockerCapabilityVersion, ExitCode: exitCode, Stdout: base64.StdEncoding.EncodeToString(stdout.Bytes()), Stderr: base64.StdEncoding.EncodeToString(stderr.Bytes())}
}

func (b *dockerCapabilityBroker) closeBroker() {
	b.close.Do(func() {
		close(b.done)
		_ = b.listener.Close()
		_ = os.Remove(b.socket)
		dockerCapabilityBrokers.Delete(b.socket)
	})
}

type boundedBuffer struct{ data []byte }

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := dockerCapabilityMaxOutput - len(b.data)
	if remaining <= 0 {
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
	}
	b.data = append(b.data, value...)
	return original, nil
}
func (b *boundedBuffer) Bytes() []byte { return b.data }

func validateDockerCapabilityArgs(args []string, sessionID, workdir string) error {
	if len(args) == 0 || len(args) > 128 {
		return errors.New("docker command is empty or too large")
	}
	command := args[0]
	switch command {
	case "version", "build", "run", "exec", "logs", "ps", "inspect", "stop", "rm", "pull", "compose":
	default:
		return fmt.Errorf("docker command %q is not allowed", command)
	}
	for _, arg := range args {
		if isDangerousDockerArg(arg) {
			return errors.New("docker command requests a host-level capability")
		}
		if strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("docker command contains invalid characters")
		}
	}
	if (command == "stop" || command == "rm") && (hasDockerFlag(args, "--all") || hasDockerFlag(args, "-a")) {
		return errors.New("bulk Docker resource operations are not allowed")
	}
	if command == "build" && len(args) > 1 {
		contextPath := args[len(args)-1]
		if !pathInsideDocker(workdir, contextPath) {
			return errors.New("docker build context must be inside the workspace")
		}
	}
	if command == "compose" {
		for index := 1; index < len(args); index++ {
			arg := args[index]
			if arg == "-f" || arg == "--file" {
				if index+1 >= len(args) || !pathInsideDocker(workdir, args[index+1]) {
					return errors.New("docker compose file must be inside the workspace")
				}
				index++
				continue
			}
			if strings.HasPrefix(arg, "--file=") && !pathInsideDocker(workdir, strings.TrimPrefix(arg, "--file=")) {
				return errors.New("docker compose file must be inside the workspace")
			}
			if filepath.IsAbs(arg) && !pathInsideDocker(workdir, arg) {
				return errors.New("docker compose file must be inside the workspace")
			}
		}
	}
	_ = sessionID
	return nil
}

func isDangerousDockerArg(arg string) bool {
	if strings.HasPrefix(arg, "--network=") {
		return strings.TrimPrefix(arg, "--network=") == "host"
	}
	for _, flag := range []string{"--privileged", "--pid", "--network", "--device", "--volume", "-v", "--mount", "-H", "--host", "--cap-add", "--security-opt", "--userns", "--cgroupns", "--ipc", "--uts", "--runtime", "--env-file", "--publish", "-p"} {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return strings.Contains(arg, "/var/run/docker.sock")
}

func scopeDockerCapabilityArgs(args []string, sessionID string) []string {
	result := append([]string(nil), args...)
	label := "com.agent-remote.session=" + sessionID
	switch result[0] {
	case "ps":
		result = append(result, "--filter", "label="+label)
	case "run":
		result = append(result, "--label", label)
		if !hasDockerFlag(result, "--name") {
			result = append(result, "--name", "ar-"+shortDigest(sessionID+strconv.FormatInt(time.Now().UnixNano(), 10), 12))
		}
	case "compose":
		if !hasDockerFlag(result, "--project-name") && !hasDockerFlag(result, "-p") {
			result = append([]string{"compose", "--project-name", "ar-" + shortDigest(sessionID, 12)}, result[1:]...)
		}
	}
	return result
}

func hasDockerFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func dockerCapabilityTarget(args []string) string {
	if len(args) < 2 {
		return ""
	}
	switch args[0] {
	case "exec", "logs", "stop", "rm", "inspect":
		valueExpected := false
		for _, arg := range args[1:] {
			if valueExpected {
				valueExpected = false
				continue
			}
			if arg == "--" {
				valueExpected = false
				continue
			}
			if strings.HasPrefix(arg, "-") {
				switch arg {
				case "-e", "--env", "-w", "--workdir", "-u", "--user", "-f", "--format", "--filter", "--platform", "--timeout":
					valueExpected = true
				}
				continue
			}
			return arg
		}
	}
	return ""
}
func pathInsideDocker(root, candidate string) bool {
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	return filepath.Clean(candidate) == filepath.Clean(root) || strings.HasPrefix(filepath.Clean(candidate), filepath.Clean(root)+string(os.PathSeparator))
}

func dockerCapabilityPaths(root string) (socket, wrapper string) {
	directory := filepath.Join(root, "docker")
	return filepath.Join(directory, "broker.sock"), filepath.Join(directory, "bin", "docker")
}

func prepareDockerCapability(root, sessionID, workdir, docker string, uid, gid int) (*dockerCapabilityBroker, string, string, error) {
	socket, wrapper := dockerCapabilityPaths(root)
	directory := filepath.Dir(socket)
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
		return nil, "", "", err
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		return nil, "", "", err
	}
	// Bubblewrap opens the wrapper as the session runtime UID. The wrapper
	// itself is read-only, but its parent must be traversable by that UID.
	if err := os.Chmod(filepath.Dir(wrapper), 0o755); err != nil {
		return nil, "", "", err
	}
	wrapperContents := strings.ReplaceAll(dockerCapabilityWrapperTemplate, "__SESSION_ID__", sessionID)
	if err := os.WriteFile(wrapper, []byte(wrapperContents), 0o755); err != nil {
		return nil, "", "", err
	}
	if err := os.Chmod(wrapper, 0o555); err != nil {
		return nil, "", "", err
	}
	if runtime.GOOS != "linux" {
		// The privileged broker is a Linux peer-credential capability. Keep the
		// wrapper in generated specs for cross-platform tests, but do not create
		// an unusable Unix listener on platforms without SO_PEERCRED.
		return nil, socket, wrapper, nil
	}
	broker, err := startDockerCapability(socket, sessionID, workdir, docker, uid, gid)
	if err != nil {
		return nil, "", "", err
	}
	return broker, socket, wrapper, nil
}

func (e Engine) stopDockerCapability(socket string) {
	if value, ok := dockerCapabilityBrokers.Load(socket); ok {
		value.(*dockerCapabilityBroker).closeBroker()
	} else if socket != "" {
		_ = os.Remove(socket)
	}
}

var _ io.Writer = (*boundedBuffer)(nil)
