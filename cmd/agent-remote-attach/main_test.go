package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
)

func TestSessionFromOriginalCommand(t *testing.T) {
	sessionID, err := sessionFromOriginalCommand("agent-remote-attach --session session-123")
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "session-123" {
		t.Fatalf("unexpected session ID %q", sessionID)
	}
}

func TestSessionFromOriginalCommandRejectsOtherCommands(t *testing.T) {
	for _, command := range []string{"", "sh -c id", "agent-remote-attach --session ../etc/passwd", "agent-remote-attach --session ok extra"} {
		if _, err := sessionFromOriginalCommand(command); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
}

func TestBindingFromOriginalCommand(t *testing.T) {
	kind, id, err := attachTargetFromOriginalCommand("agent-remote-attach --binding account-123")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "binding" || id != "account-123" {
		t.Fatalf("unexpected target %q %q", kind, id)
	}
}

func TestTunnelFromOriginalCommand(t *testing.T) {
	forwardID, err := tunnelFromOriginalCommand("agent-remote-tunnel --forward 123e4567-e89b-12d3-a456-426614174000 --protocol 1")
	if err != nil {
		t.Fatal(err)
	}
	if forwardID != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("unexpected forward ID %q", forwardID)
	}
}

func TestTunnelFromOriginalCommandRejectsUnsafeRequests(t *testing.T) {
	commands := []string{
		"agent-remote-tunnel --forward forward-1 --protocol 2",
		"agent-remote-tunnel --forward ../forward --protocol 1",
		"agent-remote-tunnel --forward forward-1 --protocol 1 extra",
		"agent-remote-tunnel --forward 'forward-1;id' --protocol 1",
		"ssh -W host:22",
	}
	for _, command := range commands {
		if _, err := tunnelFromOriginalCommand(command); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
}

func TestTunnelFromOriginalCommandRejectsEmptyAndOversizedIDs(t *testing.T) {
	for _, command := range []string{
		"agent-remote-tunnel --forward --protocol 1",
		"agent-remote-tunnel --forward " + strings.Repeat("a", 129) + " --protocol 1",
	} {
		if _, err := tunnelFromOriginalCommand(command); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
}

func TestRunRoutesExactTunnelCommandWithoutStartingGatewayInDryRun(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		ServerURL:              "https://control.example.test",
		NodeID:                 "node-1",
		NodeToken:              "node-token",
		AllowedRuntimeBackends: []string{"native"},
	}.WithDefaults()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-tunnel --forward forward-1 --protocol 1")
	if err := run([]string{"--config", configPath, "--device", "device-1", "--ssh-key", "key-1", "--dry-run"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunTunnelRequiresForcedCommandSSHKeyIdentity(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		ServerURL:              "https://control.example.test",
		NodeID:                 "node-1",
		NodeToken:              "node-token",
		AllowedRuntimeBackends: []string{"native"},
	}.WithDefaults()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-tunnel --forward forward-1 --protocol 1")
	err = run([]string{"--config", configPath, "--device", "device-1", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "SSH key is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStdioConnectionImplementsDuplexNetConnection(t *testing.T) {
	reader, readPeer := net.Pipe()
	writer, writePeer := net.Pipe()
	defer readPeer.Close()
	defer writePeer.Close()
	connection := &stdioConnection{reader: reader, writer: writer}

	writeDone := make(chan error, 1)
	go func() {
		_, err := connection.Write([]byte("out"))
		writeDone <- err
	}()
	buffer := make([]byte, 3)
	if _, err := io.ReadFull(writePeer, buffer); err != nil || string(buffer) != "out" {
		t.Fatalf("unexpected outbound payload %q: %v", buffer, err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := readPeer.Write([]byte("in"))
		readDone <- err
	}()
	if _, err := io.ReadFull(connection, buffer[:2]); err != nil || string(buffer[:2]) != "in" {
		t.Fatalf("unexpected inbound payload %q: %v", buffer[:2], err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	if err := connection.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if connection.LocalAddr().Network() != "stdio" || connection.LocalAddr().String() != "local-stdio" {
		t.Fatalf("unexpected local address %v", connection.LocalAddr())
	}
	if connection.RemoteAddr().String() != "remote-stdio" {
		t.Fatalf("unexpected remote address %v", connection.RemoteAddr())
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultConfigPathUsesEnvironment(t *testing.T) {
	t.Setenv("AGENT_REMOTE_NODE_CONFIG", "/custom/config.json")
	if path := defaultConfigPath(); path != "/custom/config.json" {
		t.Fatalf("unexpected config path %q", path)
	}
}

func TestChildExitErrorCanPreserveMissingCommandStatus(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 127").Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("expected exec exit error, got %v", err)
	}
	if exitError.ExitCode() != 127 {
		t.Fatalf("unexpected exit code %d", exitError.ExitCode())
	}
}
