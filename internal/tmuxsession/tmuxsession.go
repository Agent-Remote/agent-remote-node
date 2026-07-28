package tmuxsession

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
)

const (
	initialWidth  = "160"
	initialHeight = "48"
)

// NewSessionArgs creates a detached session whose application starts after the
// first client attaches. This lets terminal applications observe the client's
// real dimensions instead of the detached session's fallback canvas.
func NewSessionArgs(binary string, socketPath string, sessionName string, command string) []string {
	args := socketArgs(socketPath)
	return append(args,
		"new-session", "-d",
		"-x", initialWidth,
		"-y", initialHeight,
		"-s", sessionName,
		waitForClientCommand(binary, socketPath, sessionName, command),
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
		// Do not manually resize the window from hooks: transient zero-sized
		// clients can otherwise force an invalid window size. Notify the pane's
		// foreground process after tmux settles, then repaint the client.
		{"set-hook", "-t", sessionName, "client-attached", "wait-for -S " + clientReadyChannel(sessionName)},
		{"set-hook", "-t", sessionName, "client-resized", resizeHookCommand(binary, socketPath)},
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

func waitForClientCommand(binary string, socketPath string, sessionName string, command string) string {
	parts := []string{shellQuote(binary)}
	if socketPath != "" {
		parts = append(parts, "-S", shellQuote(socketPath))
	}
	parts = append(parts, "wait-for", shellQuote(clientReadyChannel(sessionName)))
	return strings.Join(parts, " ") + " && sleep 0.1 && exec " + command
}

func clientReadyChannel(sessionName string) string {
	digest := sha256.Sum256([]byte(sessionName))
	return fmt.Sprintf("agent-remote-client-%x", digest[:8])
}

func resizeHookCommand(binary string, socketPath string) string {
	return "run-shell -b " + shellQuote(resizeShellCommand(binary, socketPath))
}

func resizeShellCommand(binary string, socketPath string) string {
	tmux := []string{shellQuote(binary)}
	if socketPath != "" {
		tmux = append(tmux, "-S", shellQuote(socketPath))
	}
	tmux = append(tmux, "refresh-client", "-S", "-t", shellQuote("#{client_name}"))
	script := strings.Join([]string{
		"sleep 0.15",
		"pgrp=$(ps -o tpgid= -p #{pane_pid} | tr -d ' ')",
		"case \"$pgrp\" in ''|*[!0-9]*) exit 0 ;; esac",
		"[ \"$pgrp\" -gt 0 ] || exit 0",
		"/bin/kill -WINCH -- \"-$pgrp\" 2>/dev/null || true",
		"sleep 0.05",
		strings.Join(tmux, " "),
	}, "; ")
	return script
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func socketArgs(socketPath string) []string {
	if socketPath == "" {
		return nil
	}
	return []string{"-S", socketPath}
}
