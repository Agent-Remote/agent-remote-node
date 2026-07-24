package tmuxsession

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewSessionArgs(t *testing.T) {
	want := []string{
		"-S", "/run/agent/tmux.sock",
		"new-session", "-d", "-x", "160", "-y", "48",
		"-s", "agent-session", "claude",
	}
	got := NewSessionArgs("/run/agent/tmux.sock", "agent-session", "claude")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NewSessionArgs() = %#v, want %#v", got, want)
	}
}

func TestNewSessionArgsWithoutSocket(t *testing.T) {
	want := []string{
		"new-session", "-d", "-x", "160", "-y", "48",
		"-s", "agent-session", "claude",
	}
	got := NewSessionArgs("", "agent-session", "claude")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NewSessionArgs() = %#v, want %#v", got, want)
	}
}

func TestAttachArgs(t *testing.T) {
	want := []string{
		"-S", "/run/agent/tmux.sock",
		"attach-session", "-d", "-t", "agent-session",
	}
	got := AttachArgs("/run/agent/tmux.sock", "agent-session")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AttachArgs() = %#v, want %#v", got, want)
	}
}

func TestAttachArgsWithoutSocket(t *testing.T) {
	want := []string{"attach-session", "-d", "-t", "agent-session"}
	got := AttachArgs("", "agent-session")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AttachArgs() = %#v, want %#v", got, want)
	}
}

func TestConfigure(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands.log")
	binary := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TMUX_TEST_LOG\"\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TEST_LOG", logPath)

	if err := Configure(binary, "/run/agent/tmux.sock", "agent-session"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-S /run/agent/tmux.sock set-option -t agent-session status off",
		"-S /run/agent/tmux.sock set-option -t agent-session focus-events on",
		"-S /run/agent/tmux.sock set-window-option -t agent-session window-size latest",
		"-S /run/agent/tmux.sock set-window-option -t agent-session aggressive-resize on",
		"-S /run/agent/tmux.sock set-option -s terminal-features xterm*:RGB",
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Configure() commands = %#v, want %#v", got, want)
	}
}
