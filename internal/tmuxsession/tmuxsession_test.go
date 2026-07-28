package tmuxsession

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewSessionArgs(t *testing.T) {
	command := waitForClientCommand("/usr/bin/tmux", "/run/agent/tmux.sock", "agent-session", "claude")
	want := []string{
		"-S", "/run/agent/tmux.sock",
		"new-session", "-d", "-x", "160", "-y", "48",
		"-s", "agent-session", command,
	}
	got := NewSessionArgs("/usr/bin/tmux", "/run/agent/tmux.sock", "agent-session", "claude")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NewSessionArgs() = %#v, want %#v", got, want)
	}
}

func TestNewSessionArgsWithoutSocket(t *testing.T) {
	command := waitForClientCommand("tmux", "", "agent-session", "claude")
	want := []string{
		"new-session", "-d", "-x", "160", "-y", "48",
		"-s", "agent-session", command,
	}
	got := NewSessionArgs("tmux", "", "agent-session", "claude")
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
		"-S /run/agent/tmux.sock set-hook -t agent-session client-attached wait-for -S " + clientReadyChannel("agent-session"),
		"-S /run/agent/tmux.sock set-hook -t agent-session client-resized refresh-client -S",
		"-S /run/agent/tmux.sock set-option -t agent-session status off",
		"-S /run/agent/tmux.sock set-option -t agent-session focus-events on",
		"-S /run/agent/tmux.sock set-window-option -t agent-session aggressive-resize off",
		"-S /run/agent/tmux.sock set-window-option -t agent-session window-size largest",
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
	socketDir, err := os.MkdirTemp("/tmp", "agent-remote-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "tmux.sock")
	if output, err := exec.Command(binary, NewSessionArgs(binary, socketPath, "agent-session", "sleep 30")...).CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(binary, "-S", socketPath, "kill-server").Run()
	})

	if err := Configure(binary, socketPath, "agent-session"); err != nil {
		t.Fatal(err)
	}
	windowSize, err := exec.Command(binary, "-S", socketPath, "show-window-options", "-v", "-t", "agent-session", "window-size").CombinedOutput()
	if err != nil {
		t.Fatalf("show window-size: %v: %s", err, windowSize)
	}
	if strings.TrimSpace(string(windowSize)) != "largest" {
		t.Fatalf("window-size = %q, want largest", strings.TrimSpace(string(windowSize)))
	}
	aggressiveResize, err := exec.Command(binary, "-S", socketPath, "show-window-options", "-v", "-t", "agent-session", "aggressive-resize").CombinedOutput()
	if err != nil {
		t.Fatalf("show aggressive-resize: %v: %s", err, aggressiveResize)
	}
	if strings.TrimSpace(string(aggressiveResize)) != "off" {
		t.Fatalf("aggressive-resize = %q, want off", strings.TrimSpace(string(aggressiveResize)))
	}
	for _, hook := range []string{"client-attached", "client-resized"} {
		output, err := exec.Command(binary, "-S", socketPath, "show-hooks", "-t", "agent-session", hook).CombinedOutput()
		if err != nil {
			t.Fatalf("show %s hook: %v: %s", hook, err, output)
		}
		if strings.Contains(string(output), "resize-window") {
			t.Fatalf("%s hook still forces a manual window size: %s", hook, output)
		}
	}
}

func TestWindowTracksControlClientResize(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	socketDir, err := os.MkdirTemp("/tmp", "agent-remote-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "tmux.sock")
	if output, err := exec.Command(binary, NewSessionArgs(binary, socketPath, "agent-session", "sleep 30")...).CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(binary, "-S", socketPath, "kill-server").Run()
	})
	if err := Configure(binary, socketPath, "agent-session"); err != nil {
		t.Fatal(err)
	}

	client := exec.Command(binary, "-C", "-S", socketPath, "attach-session", "-d", "-f", "!ignore-size", "-t", "agent-session")
	stdin, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = client.Wait()
	})

	for _, size := range []struct {
		width  int
		height int
	}{{100, 30}, {42, 9}, {12, 4}} {
		if _, err := fmt.Fprintf(stdin, "refresh-client -C %d,%d\n", size.width, size.height); err != nil {
			t.Fatalf("resize control client: %v", err)
		}
		want := fmt.Sprintf("%dx%d", size.width, size.height)
		waitForWindowSize(t, binary, socketPath, want)
	}
}

func TestApplicationStartsAtAttachedClientSize(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	socketDir, err := os.MkdirTemp("/tmp", "agent-remote-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "tmux.sock")
	resultPath := filepath.Join(socketDir, "size.txt")
	command := fmt.Sprintf("stty size > %s; sleep 30", shellQuote(resultPath))
	if output, err := exec.Command(binary, NewSessionArgs(binary, socketPath, "agent-session", command)...).CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command(binary, "-S", socketPath, "kill-server").Run()
	})
	if err := Configure(binary, socketPath, "agent-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
		t.Fatalf("application started before a client attached: %v", err)
	}

	client := exec.Command(binary, "-C", "-S", socketPath, "attach-session", "-d", "-f", "!ignore-size", "-t", "agent-session")
	stdin, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = client.Wait()
	})
	if _, err := fmt.Fprintln(stdin, "refresh-client -C 123,37"); err != nil {
		t.Fatal(err)
	}
	waitForFileContent(t, resultPath, "37 123")
}

func waitForWindowSize(t *testing.T, binary string, socketPath string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		output, err := exec.Command(binary, "-S", socketPath, "display-message", "-p", "-t", "agent-session:0", "#{window_width}x#{window_height}").CombinedOutput()
		if err == nil {
			got = strings.TrimSpace(string(output))
			if got == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("window size = %q, want %q", got, want)
}

func waitForFileContent(t *testing.T, path string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			got = strings.TrimSpace(string(data))
			if got == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file content = %q, want %q", got, want)
}
