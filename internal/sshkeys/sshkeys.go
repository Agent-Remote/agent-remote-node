package sshkeys

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const beginMarker = "# BEGIN agent-remote managed keys"
const endMarker = "# END agent-remote managed keys"
const deviceBeginPrefix = "# BEGIN agent-remote device "
const deviceEndPrefix = "# END agent-remote device "

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
func Sync(path string, attachBinary string, attachConfig string, payload SyncPayload) error {
	if path == "" {
		return fmt.Errorf("authorized_keys path is required")
	}
	if !validDeviceID(payload.DeviceID) {
		return fmt.Errorf("device ID is required and must contain only safe identifier characters")
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
	base, managed := splitManagedBlock(string(existing))
	devices := parseManagedDevices(managed)
	entries := make([]string, 0, len(payload.SSHKeys))
	for _, key := range payload.SSHKeys {
		line := RenderEntry(attachBinary, attachConfig, key)
		if line != "" {
			entries = append(entries, line)
		}
	}
	if len(entries) == 0 {
		delete(devices, payload.DeviceID)
	} else {
		devices[payload.DeviceID] = entries
	}

	var block bytes.Buffer
	block.WriteString(beginMarker + "\n")
	deviceIDs := make([]string, 0, len(devices))
	for deviceID := range devices {
		deviceIDs = append(deviceIDs, deviceID)
	}
	sort.Strings(deviceIDs)
	for _, deviceID := range deviceIDs {
		block.WriteString(deviceBeginPrefix + deviceID + "\n")
		for _, line := range devices[deviceID] {
			block.WriteString(line + "\n")
		}
		block.WriteString(deviceEndPrefix + deviceID + "\n")
	}
	block.WriteString(endMarker + "\n")
	var output string
	if strings.TrimSpace(base) == "" {
		output = block.String()
	} else {
		output = strings.TrimRight(base, "\n") + "\n" + block.String()
	}
	return writeFileAtomic(path, []byte(output))
}

// RenderEntry renders one forced-command authorized_keys line.
func RenderEntry(attachBinary string, attachConfig string, key Entry) string {
	publicKey := strings.TrimSpace(key.PublicKey)
	if publicKey == "" {
		return ""
	}
	command := strings.ReplaceAll(key.ForcedCommand, "\\", "\\\\")
	command = strings.ReplaceAll(command, "\"", "\\\"")
	return fmt.Sprintf(
		"command=\"%s\",no-X11-forwarding,no-port-forwarding,no-user-rc %s",
		commandWithBinary(attachBinary, attachConfig, command),
		publicKey,
	)
}

func commandWithBinary(attachBinary string, attachConfig string, forcedCommand string) string {
	if strings.HasPrefix(forcedCommand, "agent-remote-attach ") &&
		(attachBinary != "agent-remote-attach" || attachConfig != "") {
		command := shellWord(attachBinary)
		if attachConfig != "" {
			command += " --config " + shellWord(attachConfig)
		}
		return command + strings.TrimPrefix(forcedCommand, "agent-remote-attach")
	}
	return forcedCommand
}

func shellWord(value string) string {
	if value != "" && strings.IndexFunc(value, func(character rune) bool {
		return !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("/._-:", character))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func splitManagedBlock(input string) (string, string) {
	start := strings.Index(input, beginMarker)
	if start < 0 {
		return input, ""
	}
	managedStart := start + len(beginMarker)
	end := strings.Index(input[managedStart:], endMarker)
	if end < 0 {
		return strings.TrimRight(input[:start], "\n") + "\n", input[managedStart:]
	}
	managedEnd := managedStart + end
	blockEnd := managedEnd + len(endMarker)
	base := strings.TrimRight(input[:start]+input[blockEnd:], "\n") + "\n"
	return base, input[managedStart:managedEnd]
}

func parseManagedDevices(managed string) map[string][]string {
	devices := map[string][]string{}
	activeDevice := ""
	for _, rawLine := range strings.Split(managed, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, deviceBeginPrefix) {
			candidate := strings.TrimSpace(strings.TrimPrefix(line, deviceBeginPrefix))
			if validDeviceID(candidate) {
				activeDevice = candidate
			}
			continue
		}
		if strings.HasPrefix(line, deviceEndPrefix) {
			activeDevice = ""
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		deviceID := activeDevice
		if deviceID == "" {
			deviceID = deviceIDFromEntry(line)
		}
		if validDeviceID(deviceID) {
			devices[deviceID] = append(devices[deviceID], line)
		}
	}
	return devices
}

func deviceIDFromEntry(line string) string {
	const flag = " --device "
	start := strings.Index(line, flag)
	if start < 0 {
		return ""
	}
	value := line[start+len(flag):]
	if end := strings.IndexAny(value, " \t\""); end >= 0 {
		value = value[:end]
	}
	return value
}

func validDeviceID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._:-", character)) {
			return false
		}
	}
	return true
}

func writeFileAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".authorized_keys-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
