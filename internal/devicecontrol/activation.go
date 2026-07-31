package devicecontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/sys/unix"
)

const (
	protocolVersion               = 1
	maximumActivationManifestSize = 16 << 10
	// MaximumDeviceSessionGeneration is the shared signed database maximum reserved for terminal revocation.
	MaximumDeviceSessionGeneration uint64 = 1<<63 - 1
	// MaximumActiveDeviceSessionGeneration is the largest generation accepted for live state.
	MaximumActiveDeviceSessionGeneration uint64 = MaximumDeviceSessionGeneration - 1
)

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	resourcePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// ActivatePayload is the zero-secret control-plane request for one device generation.
type ActivatePayload struct {
	ProtocolVersion   int    `json:"protocol_version"`
	UserID            string `json:"user_id"`
	DeviceID          string `json:"device_id"`
	ToolSessionID     string `json:"tool_session_id"`
	DeviceSessionID   string `json:"device_session_id"`
	NodeID            string `json:"node_id"`
	Platform          string `json:"platform"`
	Generation        uint64 `json:"generation"`
	ExpiresAt         string `json:"expires_at"`
	RuntimeBackend    string `json:"runtime_backend"`
	RuntimeResourceID string `json:"runtime_resource_id,omitempty"`
}

// DeactivatePayload identifies the terminal generation that revokes local activation state.
type DeactivatePayload struct {
	DeviceSessionID string `json:"device_session_id"`
	ToolSessionID   string `json:"tool_session_id"`
	Generation      uint64 `json:"generation"`
}

// ContextPayload carries the active generation binding and its short authorization lease.
type ContextPayload struct {
	ProtocolVersion int    `json:"protocol_version"`
	UserID          string `json:"user_id"`
	DeviceID        string `json:"device_id"`
	ToolSessionID   string `json:"tool_session_id"`
	DeviceSessionID string `json:"device_session_id"`
	NodeID          string `json:"node_id"`
	Platform        string `json:"platform"`
	Generation      uint64 `json:"generation"`
	LeaseUntil      string `json:"lease_until"`
}

// DecodeActivatePayload strictly validates an activation task without accepting paths or secrets.
func DecodeActivatePayload(payload map[string]any, expectedNodeID string, now time.Time) (ActivatePayload, error) {
	var decoded ActivatePayload
	if err := decodeStrict(payload, &decoded); err != nil {
		return ActivatePayload{}, err
	}
	if decoded.ProtocolVersion != protocolVersion {
		return ActivatePayload{}, errors.New("unsupported device-control protocol version")
	}
	for name, value := range map[string]string{
		"user_id": decoded.UserID, "device_id": decoded.DeviceID,
		"tool_session_id": decoded.ToolSessionID, "device_session_id": decoded.DeviceSessionID,
		"node_id": decoded.NodeID,
	} {
		if !uuidPattern.MatchString(value) {
			return ActivatePayload{}, fmt.Errorf("%s must be a canonical UUID", name)
		}
	}
	if decoded.NodeID != expectedNodeID {
		return ActivatePayload{}, errors.New("node_id does not match this node")
	}
	if decoded.Platform != "macos" {
		return ActivatePayload{}, errors.New("platform must be macos")
	}
	if decoded.Generation == 0 || decoded.Generation > MaximumActiveDeviceSessionGeneration {
		return ActivatePayload{}, errors.New("generation is outside the active session range")
	}
	if decoded.RuntimeBackend != "native" && decoded.RuntimeBackend != "docker_sandbox" {
		return ActivatePayload{}, errors.New("runtime_backend is unsupported")
	}
	if decoded.RuntimeResourceID != "" && !resourcePattern.MatchString(decoded.RuntimeResourceID) {
		return ActivatePayload{}, errors.New("runtime_resource_id is invalid")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, decoded.ExpiresAt)
	if err != nil || !expiresAt.After(now) {
		return ActivatePayload{}, errors.New("expires_at must be a future RFC3339 timestamp")
	}
	return decoded, nil
}

// DecodeDeactivatePayload strictly validates a device-control deactivation task.
func DecodeDeactivatePayload(payload map[string]any) (DeactivatePayload, error) {
	var decoded DeactivatePayload
	if err := decodeStrict(payload, &decoded); err != nil {
		return DeactivatePayload{}, err
	}
	if !uuidPattern.MatchString(decoded.DeviceSessionID) {
		return DeactivatePayload{}, errors.New("device_session_id must be a canonical UUID")
	}
	if !uuidPattern.MatchString(decoded.ToolSessionID) {
		return DeactivatePayload{}, errors.New("tool_session_id must be a canonical UUID")
	}
	if decoded.Generation == 0 || decoded.Generation > MaximumDeviceSessionGeneration {
		return DeactivatePayload{}, errors.New("generation is outside the device session range")
	}
	return decoded, nil
}

// DecodeContextPayload strictly validates a short-lived proxy context update.
func DecodeContextPayload(payload map[string]any, expectedNodeID string, now time.Time) (ContextPayload, error) {
	var decoded ContextPayload
	if err := decodeStrict(payload, &decoded); err != nil {
		return ContextPayload{}, err
	}
	if decoded.ProtocolVersion != protocolVersion {
		return ContextPayload{}, errors.New("unsupported device-control protocol version")
	}
	for name, value := range map[string]string{
		"user_id": decoded.UserID, "device_id": decoded.DeviceID,
		"tool_session_id": decoded.ToolSessionID, "device_session_id": decoded.DeviceSessionID,
		"node_id": decoded.NodeID,
	} {
		if !uuidPattern.MatchString(value) {
			return ContextPayload{}, fmt.Errorf("%s must be a canonical UUID", name)
		}
	}
	if expectedNodeID != "" && decoded.NodeID != expectedNodeID {
		return ContextPayload{}, errors.New("node_id does not match this node")
	}
	if decoded.Platform != "macos" || decoded.Generation == 0 || decoded.Generation > MaximumActiveDeviceSessionGeneration {
		return ContextPayload{}, errors.New("context platform or generation is invalid")
	}
	leaseUntil, err := time.Parse(time.RFC3339Nano, decoded.LeaseUntil)
	if err != nil || !leaseUntil.After(now) || leaseUntil.After(now.Add(5*time.Minute+30*time.Second)) {
		return ContextPayload{}, errors.New("lease_until is outside the short lease window")
	}
	return decoded, nil
}

// Activate atomically stores one validated generation beneath the configured managed root.
func Activate(root string, payload ActivatePayload) error {
	if err := prepareRoot(root); err != nil {
		return err
	}
	path := manifestPath(root, payload.DeviceSessionID)
	if current, err := loadManifest(path); err == nil {
		if current.Generation > payload.Generation {
			return errors.New("activation generation is stale")
		}
		if current.Generation == payload.Generation {
			if current != payload {
				return errors.New("activation binding changed within a generation")
			}
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".activation-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
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

// Deactivate removes only an older activated generation for the exact device session.
func Deactivate(root string, payload DeactivatePayload) (bool, error) {
	if err := prepareRoot(root); err != nil {
		return false, err
	}
	path := manifestPath(root, payload.DeviceSessionID)
	current, err := loadManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.ToolSessionID != payload.ToolSessionID {
		return false, errors.New("tool_session_id does not match activation")
	}
	if current.Generation > payload.Generation {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, nil
}

func decodeStrict(payload map[string]any, destination any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("task payload must contain one JSON object")
	}
	return nil
}

func prepareRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) {
		return errors.New("device-control root must be absolute")
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return errors.New("device-control root is unsafe")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(root, 0o700)
}

func manifestPath(root string, deviceSessionID string) string {
	return filepath.Join(root, deviceSessionID+".json")
}

func loadManifest(path string) (ActivatePayload, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ActivatePayload{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		unix.Close(fd)
		return ActivatePayload{}, errors.New("device-control activation file could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ActivatePayload{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maximumActivationManifestSize {
		return ActivatePayload{}, errors.New("device-control activation file is unsafe")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumActivationManifestSize+1))
	if err != nil {
		return ActivatePayload{}, err
	}
	if len(data) > maximumActivationManifestSize {
		return ActivatePayload{}, errors.New("device-control activation file is too large")
	}
	var payload ActivatePayload
	if err := decodeStrictJSONObject(data, &payload, "device-control activation file"); err != nil {
		return ActivatePayload{}, err
	}
	return payload, nil
}
