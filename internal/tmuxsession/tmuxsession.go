package tmuxsession

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	initialWidth  = "160"
	initialHeight = "48"
)

// NewSessionArgs creates a detached session whose application starts after the
// first client attaches. This lets terminal applications observe the client's
// real dimensions instead of the detached session's fallback canvas.
func NewSessionArgs(binary string, socketPath string, sessionName string, command string) []string {
	args := append([]string{"-f", "/dev/null"}, socketArgs(socketPath)...)
	return append(args,
		"start-server", ";", "set-option", "-g", "history-limit", "20000", ";",
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
	return configure(binary, socketPath, sessionName, nil)
}

// ConfigureAs applies managed policy without giving the tmux client host privilege.
func ConfigureAs(binary, socketPath, sessionName string, uid, gid int) error {
	return configure(binary, socketPath, sessionName, &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}})
}

func configure(binary, socketPath, sessionName string, credential *syscall.Credential) error {
	run := func(args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		if credential != nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tmux display setup failed: %w", err)
		}
		return nil
	}
	commands := restrictedBindings(binary, socketPath)
	commands = append(commands, [][]string{
		// Do not manually resize the window from hooks: transient zero-sized
		// clients can otherwise force an invalid window size. Notify the pane's
		// foreground process after tmux settles, then repaint the client.
		{"set-hook", "-t", sessionName, "client-attached", "wait-for -S " + clientReadyChannel(sessionName)},
		{"set-hook", "-t", sessionName, "client-resized", resizeHookCommand(binary, socketPath)},
		{"set-option", "-t", sessionName, "status", "off"},
		{"set-option", "-t", sessionName, "focus-events", "on"},
		{"set-option", "-t", sessionName, "mouse", "on"},
		{"set-option", "-t", sessionName, "prefix", "C-b"},
		{"set-option", "-t", sessionName, "prefix2", "None"},
		{"set-option", "-t", sessionName, "key-table", "root"},
		// SSH may batch ordinary keystrokes. Timing must not turn prefix keys
		// into application text; explicit bracketed paste remains supported.
		{"set-option", "-t", sessionName, "assume-paste-time", "0"},
		{"set-option", "-t", sessionName, "destroy-unattached", "off"},
		{"set-option", "-s", "escape-time", "10"},
		// Only tmux's user selections write the outer clipboard. Applications
		// cannot create clipboard buffers through their own OSC 52.
		{"set-option", "-s", "set-clipboard", "external"},
		// The CLI consumes OSC 52 even when the desktop terminal lacks support.
		// A fixed array slot keeps repeated attaches idempotent and preserves defaults.
		{"set-option", "-s", "terminal-overrides[100]", `*:Ms=\E]52;%p1%s;%p2%s\007`},
		// Drag selects text even when Claude enables mouse reporting; clicks and
		// wheel events retain the standard tmux/application bindings.
		{"bind-key", "-T", "root", "MouseDrag1Pane", "copy-mode", "-M"},
		{"bind-key", "-T", "copy-mode", "MouseDragEnd1Pane", "run-shell", "-b", copySelectionCommand(binary, socketPath)},
		{"bind-key", "-T", "copy-mode-vi", "MouseDragEnd1Pane", "run-shell", "-b", copySelectionCommand(binary, socketPath)},
		{"set-window-option", "-t", sessionName, "aggressive-resize", "off"},
		{"set-window-option", "-t", sessionName, "window-size", "largest"},
	}...)
	args := socketArgs(socketPath)
	for i, command := range commands {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, command...)
	}
	if err := run(args); err != nil {
		return err
	}

	// terminal-features was added after the core display options. Keep RGB
	// enhancement best-effort so older distribution tmux builds can attach.
	rgbArgs := append(socketArgs(socketPath), "set-option", "-s", "terminal-features[100]", "xterm*:RGB")
	_ = run(rgbArgs)
	// Negotiate extended keys only when the application requests them. Older
	// tmux builds keep their normal key handling if this option is unavailable.
	_ = run(append(socketArgs(socketPath), "set-option", "-s", "extended-keys", "on"))
	return nil
}

func copySelectionCommand(binary string, socketPath string) string {
	parts := []string{shellQuote(binary)}
	if socketPath != "" {
		parts = append(parts, "-S", shellQuote(socketPath))
	}
	// SSH may batch drag and release in one read. tmux suppresses clipboard
	// writes while the pane has PANE_REDRAW set, so let that redraw complete.
	// The pane target is tmux-generated, and clipboard text never enters argv.
	return "sleep 0.05; exec " + strings.Join(parts, " ") +
		" send-keys -X -t '#{pane_id}' copy-selection-and-cancel " + shellQuote(";") +
		" display-message -d 1000 -t '#{pane_id}' 'Selection sent to clipboard' >/dev/null 2>&1"
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
	tmuxPrefix := strings.Join(tmux, " ")
	sessionTarget := shellQuote("#{session_id}")
	clientTarget := shellQuote("#{client_name}")
	tokenOption := "@agent-remote-resize-token"
	// Native sessions put Claude behind two bwrap processes, so tmux's
	// foreground process group belongs to bwrap. Find Claude's real group.
	script := strings.Join([]string{
		"token=$$",
		tmuxPrefix + " set-option -q -t " + sessionTarget + " " + tokenOption + " \"$token\"",
		"sleep 0.2",
		"[ \"$(" + tmuxPrefix + " show-option -qv -t " + sessionTarget + " " + tokenOption + ")\" = \"$token\" ] || exit 0",
		"pgrp=''",
		"queue='#{pane_pid}'",
		"depth=0",
		"while [ -n \"$queue\" ] && [ \"$depth\" -lt 8 ] && [ -z \"$pgrp\" ]; do next=''; for parent in $queue; do for child in $(pgrep -P \"$parent\" 2>/dev/null); do command=$(ps -o comm= -p \"$child\" | tr -d ' '); case \"$command\" in claude|claude-*) pgrp=$(ps -o pgid= -p \"$child\" | tr -d ' '); break ;; esac; next=\"$next $child\"; done; [ -n \"$pgrp\" ] && break; done; queue=$next; depth=$((depth + 1)); done",
		"[ -n \"$pgrp\" ] || pgrp=$(ps -o tpgid= -p #{pane_pid} | tr -d ' ')",
		"case \"$pgrp\" in ''|*[!0-9]*) exit 0 ;; esac",
		"[ \"$pgrp\" -gt 0 ] || exit 0",
		"/bin/kill -WINCH -- \"-$pgrp\" 2>/dev/null || true",
		"sleep 0.05",
		tmuxPrefix + " refresh-client -t " + clientTarget,
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
