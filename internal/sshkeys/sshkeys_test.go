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
	if err := Sync(path, "/usr/local/bin/agent-remote-attach", payload); err != nil {
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
	if !strings.Contains(text, "command=\"/usr/local/bin/agent-remote-attach --session sess_1 --device dev_1\"") {
		t.Fatalf("forced command missing: %s", text)
	}
	if !strings.Contains(text, "no-pty") {
		t.Fatal("restrictive options missing")
	}
}
