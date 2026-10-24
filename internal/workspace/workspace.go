package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const controlPlaneRoot = "/var/lib/agent-remote/users"

// PreparePayload describes a prepare_workspace task payload.
type PreparePayload struct {
	UserID        string `json:"user_id"`
	WorkspaceID   string `json:"workspace_id"`
	SyncSessionID string `json:"sync_session_id"`
	RemotePath    string `json:"remote_path"`
}

// PrepareResult describes the prepared workspace directory.
type PrepareResult struct {
	RemotePath string `json:"remote_path"`
	MarkerPath string `json:"marker_path"`
}

// DecodePreparePayload converts a generic node task payload into a typed payload.
func DecodePreparePayload(payload map[string]any) (PreparePayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return PreparePayload{}, err
	}
	var decoded PreparePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return PreparePayload{}, err
	}
	if decoded.UserID == "" {
		return PreparePayload{}, errors.New("user_id is required")
	}
	if decoded.WorkspaceID == "" {
		return PreparePayload{}, errors.New("workspace_id is required")
	}
	if decoded.SyncSessionID == "" {
		return PreparePayload{}, errors.New("sync_session_id is required")
	}
	return decoded, nil
}

// Prepare creates the remote workspace directory and writes a small marker file.
func Prepare(root string, payload PreparePayload) (PrepareResult, error) {
	directory, err := OpenDirectory(root, payload)
	if err != nil {
		return PrepareResult{}, err
	}
	defer directory.Close()
	return directory.WriteMarker(payload)
}

var managedID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func validatePayload(payload PreparePayload) error {
	if !managedID.MatchString(payload.UserID) || !managedID.MatchString(payload.WorkspaceID) || !managedID.MatchString(payload.SyncSessionID) {
		return errors.New("workspace identity is invalid")
	}
	return nil
}

// Directory retains the exact managed ancestor and workspace directory handles.
// Close releases all handles; callers must not close individual entries.
type Directory struct {
	Path    string
	Parents []*os.File
	File    *os.File
}

// Close releases the pinned workspace and ancestor handles.
func (d *Directory) Close() {
	if d.File != nil {
		_ = d.File.Close()
	}
	for _, parent := range d.Parents {
		_ = parent.Close()
	}
}

func resolveRemotePath(root string, payload PreparePayload) (string, error) {
	if err := validatePayload(payload); err != nil {
		return "", err
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	remotePath := payload.RemotePath
	if remotePath == "" {
		remotePath = filepath.Join(cleanRoot, payload.UserID, "workspaces", payload.WorkspaceID, "files")
	}
	cleanPath := filepath.Clean(remotePath)
	if mappedPath, ok := mapControlPlanePath(cleanRoot, cleanPath); ok {
		cleanPath = mappedPath
	}
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(cleanRoot, cleanPath)
	}
	if cleanPath != filepath.Join(cleanRoot, payload.UserID, "workspaces", payload.WorkspaceID, "files") {
		return "", fmt.Errorf("remote_path %s is outside workspace_root %s", cleanPath, cleanRoot)
	}
	return cleanPath, nil
}

func mapControlPlanePath(root string, candidate string) (string, bool) {
	cleanControlRoot := filepath.Clean(controlPlaneRoot)
	if filepath.Clean(root) == cleanControlRoot {
		return "", false
	}
	if !isPathInside(cleanControlRoot, candidate) {
		return "", false
	}
	relative, err := filepath.Rel(cleanControlRoot, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return "", false
	}
	return filepath.Join(root, relative), true
}

func isPathInside(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(os.PathSeparator))
}
