package tmuxsession

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	initialWidth  = "160"
	initialHeight = "48"
)

// NewSessionArgs gives detached terminal applications a useful canvas before
// the first SSH client attaches. tmux will resize it to the latest client.
func NewSessionArgs(socketPath string, sessionName string, command string) []string {
	args := socketArgs(socketPath)
	return append(args,
		"new-session", "-d",
		"-x", initialWidth,
		"-y", initialHeight,
		"-s", sessionName,
		command,
	)
}

// AttachArgs makes the newly attached terminal the only client for the
// session. This prevents a disconnected or background terminal from keeping
// a full-screen application at an obsolete size when users switch terminals.
func AttachArgs(socketPath string, sessionName string) []string {
	args := socketArgs(socketPath)
	return append(args, "attach-session", "-d", "-f", "!ignore-size", "-t", sessionName)
}

// Configure removes tmux chrome and makes the session follow its sole active
// terminal client, which keeps full-screen agents visually native over SSH.
func Configure(binary string, socketPath string, sessionName string) error {
	commands := [][]string{
		// Older releases installed explicit resize hooks. Remove them before
		// changing the policy so transient zero-sized clients cannot force an
		// invalid manual window size during a terminal resize.
		{"set-hook", "-u", "-t", sessionName, "client-attached"},
		{"set-hook", "-u", "-t", sessionName, "client-resized"},
		{"set-option", "-t", sessionName, "status", "off"},
		{"set-option", "-t", sessionName, "focus-events", "on"},
		{"set-window-option", "-t", sessionName, "aggressive-resize", "off"},
		{"set-window-option", "-t", sessionName, "window-size", "largest"},
	}
	for _, command := range commands {
		args := append(socketArgs(socketPath), command...)
		if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux display setup failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	// terminal-features was added after the core display options. Keep RGB
	// enhancement best-effort so older distribution tmux builds can attach.
	rgbArgs := append(socketArgs(socketPath), "set-option", "-s", "terminal-features", "xterm*:RGB")
	_ = exec.Command(binary, rgbArgs...).Run()
	return nil
}

func socketArgs(socketPath string) []string {
	if socketPath == "" {
		return nil
	}
	return []string{"-S", socketPath}
}
