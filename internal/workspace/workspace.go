package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	remotePath, err := resolveRemotePath(root, payload)
	if err != nil {
		return PrepareResult{}, err
	}
	if err := os.MkdirAll(remotePath, 0o700); err != nil {
		return PrepareResult{}, err
	}
	markerPath := filepath.Join(remotePath, ".agent-remote-workspace.json")
	marker := map[string]string{
		"user_id":         payload.UserID,
		"workspace_id":    payload.WorkspaceID,
		"sync_session_id": payload.SyncSessionID,
		"prepared_at":     time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return PrepareResult{}, err
	}
	if err := os.WriteFile(markerPath, append(data, '\n'), 0o600); err != nil {
		return PrepareResult{}, err
	}
	return PrepareResult{RemotePath: remotePath, MarkerPath: markerPath}, nil
}

func resolveRemotePath(root string, payload PreparePayload) (string, error) {
	cleanRoot := filepath.Clean(root)
	remotePath := payload.RemotePath
	if remotePath == "" {
		remotePath = filepath.Join(cleanRoot, payload.UserID, "workspaces", payload.WorkspaceID, "files")
	}
	cleanPath := filepath.Clean(remotePath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(cleanRoot, cleanPath)
	}
	if !isPathInside(cleanRoot, cleanPath) {
		return "", fmt.Errorf("remote_path %s is outside workspace_root %s", cleanPath, cleanRoot)
	}
	return cleanPath, nil
}

func isPathInside(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(os.PathSeparator))
}
