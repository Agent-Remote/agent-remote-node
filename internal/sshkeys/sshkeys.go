package sshkeys

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const beginMarker = "# BEGIN agent-remote managed keys"
const endMarker = "# END agent-remote managed keys"

// Entry describes one managed authorized_keys entry.
type Entry struct {
	ID            string `json:"id"`
	PublicKey     string `json:"public_key"`
	ForcedCommand string `json:"forced_command"`
}

// SyncPayload describes a sync_ssh_keys task payload.
type SyncPayload struct {
	DeviceID           string  `json:"device_id"`
	SessionID          string  `json:"session_id"`
	SSHUser            string  `json:"ssh_user"`
	AuthorizedKeysPath *string `json:"authorized_keys_path"`
	SSHKeys            []Entry `json:"ssh_keys"`
}

// DecodePayload decodes a generic task payload.
func DecodePayload(payload map[string]any) (SyncPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return SyncPayload{}, err
	}
	var decoded SyncPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return SyncPayload{}, err
	}
	return decoded, nil
}

// Sync writes the managed authorized_keys block.
func Sync(path string, attachBinary string, payload SyncPayload) error {
	if path == "" {
		return fmt.Errorf("authorized_keys path is required")
	}
	if attachBinary == "" {
		attachBinary = "agent-remote-attach"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	base := stripManagedBlock(string(existing))
	var block bytes.Buffer
	block.WriteString(beginMarker + "\n")
	for _, key := range payload.SSHKeys {
		line := RenderEntry(attachBinary, key)
		if line != "" {
			block.WriteString(line + "\n")
		}
	}
	block.WriteString(endMarker + "\n")
	var output string
	if strings.TrimSpace(base) == "" {
		output = block.String()
	} else {
		output = strings.TrimRight(base, "\n") + "\n" + block.String()
	}
	return os.WriteFile(path, []byte(output), 0o600)
}

// RenderEntry renders one forced-command authorized_keys line.
func RenderEntry(attachBinary string, key Entry) string {
	publicKey := strings.TrimSpace(key.PublicKey)
	if publicKey == "" {
		return ""
	}
	command := strings.ReplaceAll(key.ForcedCommand, "\\", "\\\\")
	command = strings.ReplaceAll(command, "\"", "\\\"")
	return fmt.Sprintf(
		"command=\"%s\",no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty %s",
		commandWithBinary(attachBinary, command),
		publicKey,
	)
}

func commandWithBinary(attachBinary string, forcedCommand string) string {
	if strings.HasPrefix(forcedCommand, "agent-remote-attach ") && attachBinary != "agent-remote-attach" {
		return attachBinary + strings.TrimPrefix(forcedCommand, "agent-remote-attach")
	}
	return forcedCommand
}

func stripManagedBlock(input string) string {
	start := strings.Index(input, beginMarker)
	if start < 0 {
		return input
	}
	end := strings.Index(input[start:], endMarker)
	if end < 0 {
		return strings.TrimRight(input[:start], "\n") + "\n"
	}
	end += start + len(endMarker)
	return strings.TrimRight(input[:start]+input[end:], "\n") + "\n"
}
