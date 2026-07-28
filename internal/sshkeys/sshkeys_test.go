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
	payload := SyncPayload{DeviceID: "dev_1", SSHKeys: []Entry{{
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

func TestSyncPreservesOtherDevicesAndRotatesCurrentDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	first := SyncPayload{DeviceID: "dev_1", SSHKeys: []Entry{{
		PublicKey:     "ssh-ed25519 AAAADEV1OLD dev1@test",
		ForcedCommand: "agent-remote-attach --device dev_1",
	}}}
	second := SyncPayload{DeviceID: "dev_2", SSHKeys: []Entry{{
		PublicKey:     "ssh-ed25519 AAAADEV2 dev2@test",
		ForcedCommand: "agent-remote-attach --device dev_2",
	}}}
	rotated := SyncPayload{DeviceID: "dev_1", SSHKeys: []Entry{{
		PublicKey:     "ssh-ed25519 AAAADEV1NEW dev1@test",
		ForcedCommand: "agent-remote-attach --device dev_1",
	}}}
	for _, payload := range []SyncPayload{first, second, rotated} {
		if err := Sync(path, "agent-remote-attach", "", payload); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "AAAADEV1OLD") {
		t.Fatal("rotated device key was retained")
	}
	for _, expected := range []string{"AAAADEV1NEW", "AAAADEV2", deviceBeginPrefix + "dev_1", deviceBeginPrefix + "dev_2"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q after device-scoped sync: %s", expected, text)
		}
	}
}

func TestSyncMigratesLegacyManagedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	legacy := beginMarker + "\n" +
		"command=\"agent-remote-attach --device dev_1\" ssh-ed25519 AAAADEV1 dev1@test\n" +
		endMarker + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := SyncPayload{DeviceID: "dev_2", SSHKeys: []Entry{{
		PublicKey:     "ssh-ed25519 AAAADEV2 dev2@test",
		ForcedCommand: "agent-remote-attach --device dev_2",
	}}}
	if err := Sync(path, "agent-remote-attach", "", payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"AAAADEV1", "AAAADEV2", deviceBeginPrefix + "dev_1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("legacy entry was not migrated: %s", text)
		}
	}
}

func TestSyncWithNoKeysRemovesOnlyCurrentDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	for _, deviceID := range []string{"dev_1", "dev_2"} {
		payload := SyncPayload{DeviceID: deviceID, SSHKeys: []Entry{{
			PublicKey:     "ssh-ed25519 AAAA" + deviceID,
			ForcedCommand: "agent-remote-attach --device " + deviceID,
		}}}
		if err := Sync(path, "agent-remote-attach", "", payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := Sync(path, "agent-remote-attach", "", SyncPayload{DeviceID: "dev_1"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "dev_1") || !strings.Contains(text, "dev_2") {
		t.Fatalf("empty sync removed the wrong device entries: %s", text)
	}
}

func TestSyncRejectsUnsafeDeviceID(t *testing.T) {
	err := Sync(filepath.Join(t.TempDir(), "authorized_keys"), "agent-remote-attach", "", SyncPayload{
		DeviceID: "dev_1\nssh-ed25519 injected",
	})
	if err == nil {
		t.Fatal("expected unsafe device ID to be rejected")
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
