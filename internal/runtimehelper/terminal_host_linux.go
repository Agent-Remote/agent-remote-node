//go:build linux

package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/tmuxsession"
	"golang.org/x/sys/unix"
)

// TerminalHost holds a privileged Docker exec outside the nonprivileged tmux
// server. Its launch input comes only from the helper's private systemd pipe.
func TerminalHost(input io.Reader, ready io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("terminal host requires root")
	}
	var launch dockerTerminalLaunch
	decoder := json.NewDecoder(io.LimitReader(input, 1024*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&launch); err != nil {
		return errors.New("invalid terminal launch")
	}
	if launch.UID <= 0 || launch.GID <= 0 || len(launch.Command) == 0 || launch.Socket == "" || launch.Bridge == "" {
		return errors.New("invalid terminal identity")
	}
	// systemd serializes hosts by the session-derived unit name. A prior host
	// killed by SIGKILL may leave its socket inode after the unit has stopped.
	if info, err := os.Lstat(launch.Bridge); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("unexpected terminal bridge file")
		}
		if err := os.Remove(launch.Bridge); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: launch.Bridge, Net: "unix"})
	if err != nil {
		return errors.New("cannot listen for managed terminal")
	}
	defer listener.Close()
	if err := os.Chown(launch.Bridge, launch.UID, launch.GID); err != nil {
		return err
	}
	if err := os.Chmod(launch.Bridge, 0o600); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() { <-ctx.Done(); _ = listener.Close() }()
	clientCommand := shellQuote(launch.RuntimeBinary) + " terminal-client --socket " + shellQuote(launch.Bridge)
	args := tmuxsession.NewSessionArgs(launch.TmuxBinary, launch.Socket, launch.Session, clientCommand)
	startCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	start := exec.CommandContext(startCtx, launch.TmuxBinary, args...)
	start.SysProcAttr = terminalCredential(launch.UID, launch.GID)
	// Do not let Docker credentials or broker nonces enter the tmux server.
	start.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "SHELL=/bin/sh", "HOME=/", "LANG=C.UTF-8", "TERM=tmux-256color"}
	err = start.Run()
	cancel()
	if err != nil {
		return errors.New("cannot start nonprivileged tmux")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cleanup, launch.TmuxBinary, "-S", launch.Socket, "kill-server")
		cmd.SysProcAttr = terminalCredential(launch.UID, launch.GID)
		_ = cmd.Run()
	}()
	if err := tmuxsession.ConfigureAs(launch.TmuxBinary, launch.Socket, launch.Session, launch.UID, launch.GID); err != nil {
		return err
	}
	if _, err := io.WriteString(ready, "ready\n"); err != nil {
		return err
	}
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			return errors.New("managed terminal listener closed")
		}
		terminal, err := receiveTerminal(connection, launch.UID)
		if err != nil {
			_ = connection.Close()
			continue
		}
		// Compare the descriptor with the trusted pane, not a client-supplied path.
		if err := checkPaneTerminal(ctx, launch, terminal); err != nil {
			_ = terminal.Close()
			_ = connection.Close()
			continue
		}
		_ = listener.Close() // A fixed command can be started exactly once.
		defer connection.Close()
		defer terminal.Close()
		return runTerminalCommand(ctx, launch, connection, terminal)
	}
}

func receiveTerminal(connection *net.UnixConn, uid int) (*os.File, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return nil, err
	}
	var peer *unix.Ucred
	var peerErr error
	if err := raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Uid != uint32(uid) {
		return nil, errors.New("unauthorized terminal peer")
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer connection.SetReadDeadline(time.Time{})
	var data [1]byte
	control := make([]byte, unix.CmsgSpace(4*8))
	n, oobn, flags, _, err := connection.ReadMsgUnix(data[:], control)
	if err != nil {
		return nil, err
	}
	messages, err := unix.ParseSocketControlMessage(control[:oobn])
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, message := range messages {
		rights, parseErr := unix.ParseUnixRights(&message)
		if parseErr == nil {
			fds = append(fds, rights...)
		}
	}
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	if n != 1 || data[0] != 'T' || flags&unix.MSG_CTRUNC != 0 || len(fds) != 1 {
		return nil, errors.New("invalid terminal descriptor message")
	}
	if _, err := unix.IoctlGetTermios(fds[0], unix.TCGETS); err != nil {
		return nil, errors.New("terminal descriptor is not a tty")
	}
	unix.CloseOnExec(fds[0])
	file := os.NewFile(uintptr(fds[0]), "managed-terminal")
	fds = nil
	return file, nil
}

func checkPaneTerminal(ctx context.Context, launch dockerTerminalLaunch, file *os.File) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, launch.TmuxBinary, "-S", launch.Socket, "display-message", "-p", "-t", launch.Session+":0.0", "#{pane_tty}")
	cmd.SysProcAttr = terminalCredential(launch.UID, launch.GID)
	out := &boundedUnitOutput{remaining: 256}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil || out.overflow {
		return errors.New("cannot inspect managed pane")
	}
	path := strings.TrimSpace(out.buffer.String())
	if !strings.HasPrefix(path, "/dev/pts/") {
		return errors.New("unexpected managed tty path")
	}
	expected, err := os.Stat(path)
	if err != nil {
		return err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(expected, actual) {
		return errors.New("descriptor does not belong to managed pane")
	}
	return nil
}

func runTerminalCommand(ctx context.Context, launch dockerTerminalLaunch, connection *net.UnixConn, terminal *os.File) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, launch.Command[0], launch.Command[1:]...)
	cmd.Env = replaceEnvironment(launch.Environment, "TERM", "tmux-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = terminal, terminal, terminal
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return errors.New("cannot start sandbox terminal command")
	}
	go func() {
		var event [1]byte
		for {
			if _, err := io.ReadFull(connection, event[:]); err != nil {
				cancel()
				return
			}
			if event[0] != 'W' {
				cancel()
				return
			}
			_ = cmd.Process.Signal(syscall.SIGWINCH)
		}
	}()
	err := cmd.Wait()
	code := byte(0)
	if err != nil {
		code = 1
	}
	_, _ = connection.Write([]byte{code})
	return nil
}

// TerminalClient passes only its terminal descriptor and resize notifications.
// It cannot choose the privileged executable, arguments, environment or session.
func TerminalClient(socket string) error {
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return errors.New("managed terminal is unavailable; reconnect or create a new session")
	}
	defer connection.Close()
	resizes := make(chan os.Signal, 1)
	signal.Notify(resizes, syscall.SIGWINCH)
	defer signal.Stop(resizes)
	if _, _, err := connection.WriteMsgUnix([]byte{'T'}, unix.UnixRights(int(os.Stdin.Fd())), nil); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		var status [1]byte
		_, err := io.ReadFull(connection, status[:])
		if err == nil && status[0] != 0 {
			err = fmt.Errorf("sandbox command exited unsuccessfully")
		}
		done <- err
	}()
	for {
		select {
		case <-resizes:
			if _, err := connection.Write([]byte{'W'}); err != nil {
				return err
			}
		case err := <-done:
			return err
		}
	}
}
