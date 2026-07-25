package tmuxsession

import (
	"os"
	"os/exec"
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
		"attach-session", "-d", "-f", "!ignore-size", "-t", "agent-session",
	}
	got := AttachArgs("/run/agent/tmux.sock", "agent-session")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AttachArgs() = %#v, want %#v", got, want)
	}
}

func TestAttachArgsWithoutSocket(t *testing.T) {
	want := []string{"attach-session", "-d", "-f", "!ignore-size", "-t", "agent-session"}
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
		"-S /run/agent/tmux.sock set-hook -t agent-session client-attached resize-window -x \"#{client_width}\" -y \"#{client_height}\"; refresh-client -t \"#{client_name}\"",
		"-S /run/agent/tmux.sock set-hook -t agent-session client-resized resize-window -x \"#{client_width}\" -y \"#{client_height}\"; refresh-client -t \"#{client_name}\"",
		"-S /run/agent/tmux.sock set-option -s terminal-features xterm*:RGB",
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Configure() commands = %#v, want %#v", got, want)
	}
}

func TestConfigureWithRealTmux(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	socketPath := filepath.Join(t.TempDir(), "tmux.sock")
	if output, err := exec.Command(binary, NewSessionArgs(socketPath, "agent-session", "sleep 30")...).CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(binary, "-S", socketPath, "kill-server").Run()
	})

	if err := Configure(binary, socketPath, "agent-session"); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"client-attached", "client-resized"} {
		output, err := exec.Command(binary, "-S", socketPath, "show-hooks", "-t", "agent-session", hook).CombinedOutput()
		if err != nil {
			t.Fatalf("show %s hook: %v: %s", hook, err, output)
		}
		if !strings.Contains(string(output), `resize-window -x "#{client_width}" -y "#{client_height}"`) {
			t.Fatalf("%s hook does not preserve client dimensions: %s", hook, output)
		}
	}
}
