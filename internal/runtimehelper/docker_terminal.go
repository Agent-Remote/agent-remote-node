package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// dockerTerminalLaunch is sent over a private pipe, never written to disk or
// placed in systemd arguments. Environment may include process-only nonces.
type dockerTerminalLaunch struct {
	TmuxBinary    string
	RuntimeBinary string
	Socket        string
	Bridge        string
	Session       string
	UID           int
	GID           int
	Command       []string
	Environment   []string
}

func dockerTerminalUnit(sessionID string) string {
	return "agent-remote-terminal-" + shortDigest(sessionID, 24) + ".service"
}

func (e Engine) prepareDockerTerminal(spec *DockerSessionSpec) error {
	previous, err := e.loadDockerSessionSpec(spec.SessionID)
	if err == nil {
		if previous.TmuxSocketPath == "" {
			return errors.New("legacy Docker terminal must be stopped before creating its replacement")
		}
		if previous.UserID != spec.UserID || previous.TmuxSessionName != spec.TmuxSessionName {
			return errors.New("Docker terminal identity changed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	identity, err := e.ensureIdentity(spec.UserID)
	if err != nil {
		return err
	}
	root := e.dockerSessionRoot(spec.SessionID)
	if err := ensureRootDirectory(root, 0o711); err != nil {
		return err
	}
	// This identity has no Docker access. The socket is never mounted in the
	// sandbox; only the authorized SSH gateway can attach to it.
	dir := filepath.Join(root, "terminal")
	if err := ensureRootDirectory(dir, 0o711); err != nil {
		return err
	}
	tmuxDir := filepath.Join(dir, "tmux")
	if err := os.Mkdir(tmuxDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(tmuxDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe Docker terminal directory")
	}
	if err := os.Chown(tmuxDir, identity.UID, identity.GID); err != nil {
		return err
	}
	spec.TmuxSocketPath = filepath.Join(tmuxDir, "tmux.sock")
	spec.TerminalUID, spec.TerminalGID = identity.UID, identity.GID
	if previous.TmuxSocketPath != "" && (previous.TerminalUID != identity.UID || previous.TerminalGID != identity.GID) {
		return errors.New("Docker terminal runtime identity changed")
	}
	return e.grantDockerTerminalTraversal(*spec)
}

func (e Engine) grantDockerTerminalTraversal(spec DockerSessionSpec) error {
	if spec.TmuxSocketPath == "" {
		return nil
	}
	for _, dir := range []string{e.config.StateRoot, filepath.Join(e.config.StateRoot, "docker-sessions"), e.dockerSessionRoot(spec.SessionID)} {
		if err := exec.Command(e.config.SetfaclPath, "-m", "u:"+strconv.Itoa(spec.TerminalUID)+":--x", dir).Run(); err != nil {
			return errors.New("cannot grant managed terminal traversal")
		}
	}
	return nil
}

func (e Engine) startDockerTerminal(spec DockerSessionSpec, command, environment []string) error {
	if spec.TmuxSocketPath == "" || spec.TerminalUID <= 0 || spec.TerminalGID <= 0 {
		return errors.New("Docker terminal isolation is unavailable")
	}
	if dockerTmuxCommand(e.config.TmuxBinaryPath, spec, "has-session", "-t", spec.TmuxSessionName).Run() == nil {
		return nil
	}
	launch := dockerTerminalLaunch{
		TmuxBinary: e.config.TmuxBinaryPath, RuntimeBinary: e.config.RuntimeBinaryPath,
		Socket: spec.TmuxSocketPath, Bridge: filepath.Join(e.dockerSessionRoot(spec.SessionID), "terminal", "bridge.sock"),
		Session: spec.TmuxSessionName, UID: spec.TerminalUID, GID: spec.TerminalGID,
		Command: command, Environment: environment,
	}
	data, err := json.Marshal(launch)
	if err != nil || len(data) > 1024*1024 {
		return errors.New("Docker terminal launch exceeds limit")
	}
	cmd := exec.Command(e.config.SystemdRunPath,
		"--quiet", "--collect", "--pipe", "--service-type=exec", "--unit", dockerTerminalUnit(spec.SessionID),
		"--property=KillMode=control-group", "--property=TimeoutStopSec=5s", "--property=LimitCORE=0",
		"--property=UMask=0077", "--property=NoNewPrivileges=yes", e.config.RuntimeBinaryPath, "terminal-host")
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	cmd.Stderr = io.Discard
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	ready := make(chan bool, 1)
	go func() {
		var message [6]byte
		_, err := io.ReadFull(out, message[:])
		ready <- err == nil && string(message[:]) == "ready\n"
		_, _ = io.Copy(io.Discard, out)
		_ = cmd.Wait()
	}()
	select {
	case ok := <-ready:
		if ok {
			return nil
		}
	case <-time.After(10 * time.Second):
	}
	_ = cmd.Process.Kill()
	return errors.Join(errors.New("managed Docker terminal did not become ready"), e.stopDockerTerminal(spec))
}

func (e Engine) stopDockerTerminal(spec DockerSessionSpec) error {
	if spec.TmuxSocketPath == "" {
		return nil // Legacy resources remain stoppable through the old tmux path.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, e.config.SystemctlPath, "stop", dockerTerminalUnit(spec.SessionID)).Run(); err != nil {
		// A collected/inactive unit is an idempotent success. A failed inspection
		// is not proof that the privileged Docker exec has stopped.
		state := exec.CommandContext(ctx, e.config.SystemctlPath, "is-active", "--quiet", dockerTerminalUnit(spec.SessionID)).Run()
		var exit *exec.ExitError
		if !errors.As(state, &exit) || (exit.ExitCode() != 3 && exit.ExitCode() != 4) {
			return errors.New("managed Docker terminal could not be stopped")
		}
	}
	return nil
}

func terminalCredential(uid, gid int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}}}
}

func dockerTmuxArgs(spec DockerSessionSpec, arguments ...string) []string {
	if spec.TmuxSocketPath == "" {
		return arguments
	}
	return append([]string{"-S", spec.TmuxSocketPath}, arguments...)
}

func dockerTmuxCommand(binary string, spec DockerSessionSpec, arguments ...string) *exec.Cmd {
	cmd := exec.Command(binary, dockerTmuxArgs(spec, arguments...)...)
	if spec.TmuxSocketPath != "" {
		cmd.SysProcAttr = terminalCredential(spec.TerminalUID, spec.TerminalGID)
	}
	return cmd
}
