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

// Configure removes tmux chrome and makes the session follow the most recent
// terminal client, which keeps full-screen agents visually native over SSH.
func Configure(binary string, socketPath string, sessionName string) error {
	commands := [][]string{
		{"set-option", "-t", sessionName, "status", "off"},
		{"set-option", "-t", sessionName, "focus-events", "on"},
		{"set-window-option", "-t", sessionName, "window-size", "latest"},
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
