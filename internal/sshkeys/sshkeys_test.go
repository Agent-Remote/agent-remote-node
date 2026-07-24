package sshkeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncWritesManagedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(path, []byte("ssh-ed25519 AAAAEXISTING existing@test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := SyncPayload{SSHKeys: []Entry{{
		ID:            "key_1",
		PublicKey:     "ssh-ed25519 AAAATEST rem@test",
		ForcedCommand: "agent-remote-attach --session sess_1 --device dev_1",
	}}}
	if err := Sync(path, "/usr/local/bin/agent-remote-attach", "/etc/agent-remote-node/config.json", payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "ssh-ed25519 AAAAEXISTING") {
		t.Fatal("existing key was not preserved")
	}
	if !strings.Contains(text, beginMarker) || !strings.Contains(text, endMarker) {
		t.Fatal("managed markers missing")
	}
	if !strings.Contains(text, "command=\"/usr/local/bin/agent-remote-attach --config /etc/agent-remote-node/config.json --session sess_1 --device dev_1\"") {
		t.Fatalf("forced command missing: %s", text)
	}
	if strings.Contains(text, "no-pty") {
		t.Fatal("interactive attach must permit a PTY")
	}
	if !strings.Contains(text, "no-X11-forwarding,no-port-forwarding,no-user-rc") {
		t.Fatal("forwarding restrictions missing")
	}
	if strings.Contains(text, "no-agent-forwarding") {
		t.Fatal("SSH agent forwarding must remain available to the verified forced command")
	}
}

func TestRenderEntryQuotesCustomConfigPath(t *testing.T) {
	line := RenderEntry("agent-remote-attach", "/custom config/config.json", Entry{
		PublicKey:     "ssh-ed25519 public-key",
		ForcedCommand: "agent-remote-attach --binding account-1 --device device-1",
	})
	if !strings.Contains(line, "agent-remote-attach --config '/custom config/config.json' --binding account-1") {
		t.Fatalf("config path missing from forced command: %s", line)
	}
}
