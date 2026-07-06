package toolaccounts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const controlPlaneRoot = "/var/lib/agent-remote/users"

// RuntimeTemplate describes the command used by a tool binding session.
type RuntimeTemplate struct {
	SandboxAgent string   `json:"sandbox_agent"`
	Command      []string `json:"command"`
	Verifier     string   `json:"verifier"`
}

// CreateBindingPayload describes a create_binding_session task payload.
type CreateBindingPayload struct {
	BindingID         string          `json:"binding_id"`
	ToolAccountID     string          `json:"tool_account_id"`
	ToolType          string          `json:"tool_type"`
	UserID            string          `json:"user_id"`
	RegionCode        string          `json:"region_code"`
	Timezone          string          `json:"timezone"`
	Locale            string          `json:"locale"`
	AccountRemotePath string          `json:"account_remote_path"`
	TmuxSessionName   string          `json:"tmux_session_name"`
	Template          RuntimeTemplate `json:"template"`
	Verifier          string          `json:"verifier"`
}

// BindingResult describes the prepared binding session.
type BindingResult struct {
	Status            string `json:"status"`
	BindingSessionID  string `json:"binding_session_id"`
	ToolAccountID     string `json:"tool_account_id"`
	ToolType          string `json:"tool_type"`
	AccountRemotePath string `json:"account_remote_path"`
	TmuxSessionName   string `json:"tmux_session_name"`
	ContainerName     string `json:"container_name"`
	MarkerPath        string `json:"marker_path"`
	TmuxStarted       bool   `json:"tmux_started"`
	Verifier          string `json:"verifier"`
}

// VerifyPayload describes a verify_tool_account task payload.
type VerifyPayload struct {
	ToolAccountID     string `json:"tool_account_id"`
	ToolType          string `json:"tool_type"`
	UserID            string `json:"user_id"`
	Verifier          string `json:"verifier"`
	AccountRemotePath string `json:"account_remote_path"`
}

// VerifyResult describes verifier output.
type VerifyResult struct {
	Verified          bool           `json:"verified"`
	ToolAccountID     string         `json:"tool_account_id"`
	ToolType          string         `json:"tool_type"`
	AccountRemotePath string         `json:"account_remote_path"`
	Metadata          map[string]any `json:"metadata"`
	Error             string         `json:"error,omitempty"`
}

// ImportConfigPayload describes an import_tool_account_config task payload.
type ImportConfigPayload struct {
	ToolAccountID     string             `json:"tool_account_id"`
	ToolType          string             `json:"tool_type"`
	UserID            string             `json:"user_id"`
	AccountRemotePath string             `json:"account_remote_path"`
	Files             []ImportConfigFile `json:"files"`
}

// ImportConfigFile describes one config file to write into the account directory.
type ImportConfigFile struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
	Mode          uint32 `json:"mode"`
}

// ImportConfigResult describes imported config files.
type ImportConfigResult struct {
	Status            string   `json:"status"`
	ToolAccountID     string   `json:"tool_account_id"`
	ToolType          string   `json:"tool_type"`
	AccountRemotePath string   `json:"account_remote_path"`
	FilesWritten      []string `json:"files_written"`
}

// DecodeCreateBindingPayload converts a generic task payload into a typed payload.
func DecodeCreateBindingPayload(payload map[string]any) (CreateBindingPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return CreateBindingPayload{}, err
	}
	var decoded CreateBindingPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return CreateBindingPayload{}, err
	}
	if decoded.BindingID == "" {
		return CreateBindingPayload{}, errors.New("binding_id is required")
	}
	if decoded.ToolAccountID == "" {
		return CreateBindingPayload{}, errors.New("tool_account_id is required")
	}
	if decoded.ToolType == "" {
		return CreateBindingPayload{}, errors.New("tool_type is required")
	}
	if decoded.UserID == "" {
		return CreateBindingPayload{}, errors.New("user_id is required")
	}
	if decoded.Template.Verifier == "" {
		decoded.Template.Verifier = decoded.Verifier
	}
	return decoded, nil
}

// DecodeVerifyPayload converts a generic verifier payload into a typed payload.
func DecodeVerifyPayload(payload map[string]any) (VerifyPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return VerifyPayload{}, err
	}
	var decoded VerifyPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return VerifyPayload{}, err
	}
	if decoded.ToolAccountID == "" {
		return VerifyPayload{}, errors.New("tool_account_id is required")
	}
	if decoded.ToolType == "" {
		return VerifyPayload{}, errors.New("tool_type is required")
	}
	if decoded.UserID == "" {
		return VerifyPayload{}, errors.New("user_id is required")
	}
	return decoded, nil
}

// DecodeImportConfigPayload converts a generic import payload into a typed payload.
func DecodeImportConfigPayload(payload map[string]any) (ImportConfigPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return ImportConfigPayload{}, err
	}
	var decoded ImportConfigPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ImportConfigPayload{}, err
	}
	if decoded.ToolAccountID == "" {
		return ImportConfigPayload{}, errors.New("tool_account_id is required")
	}
	if decoded.ToolType == "" {
		return ImportConfigPayload{}, errors.New("tool_type is required")
	}
	if decoded.UserID == "" {
		return ImportConfigPayload{}, errors.New("user_id is required")
	}
	if len(decoded.Files) == 0 {
		return ImportConfigPayload{}, errors.New("files are required")
	}
	return decoded, nil
}

// PrepareBinding creates the account config archive directory and binding shell.
func PrepareBinding(root string, dockerBinary string, tmuxBinary string, payload CreateBindingPayload) (BindingResult, error) {
	accountPath, err := resolveAccountPath(root, payload.UserID, payload.ToolType, payload.ToolAccountID, payload.AccountRemotePath)
	if err != nil {
		return BindingResult{}, err
	}
	for _, dir := range []string{
		accountPath,
		filepath.Join(accountPath, ".claude"),
		filepath.Join(accountPath, "cache"),
		filepath.Join(accountPath, "workspace"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return BindingResult{}, err
		}
	}
	claudeJSONPath := filepath.Join(accountPath, ".claude.json")
	if err := ensureFile(claudeJSONPath, []byte("{}\n")); err != nil {
		return BindingResult{}, err
	}
	markerPath := filepath.Join(accountPath, ".agent-remote-tool-account.json")
	marker := map[string]any{
		"binding_id":      payload.BindingID,
		"container_name":  containerName(payload.ToolAccountID),
		"config_dir":      filepath.Join(accountPath, ".claude"),
		"tool_account_id": payload.ToolAccountID,
		"tool_type":       payload.ToolType,
		"user_id":         payload.UserID,
		"region_code":     payload.RegionCode,
		"timezone":        payload.Timezone,
		"locale":          payload.Locale,
		"sandbox_agent":   sandboxAgent(payload),
		"verifier":        payload.Verifier,
		"prepared_at":     time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return BindingResult{}, err
	}
	if err := os.WriteFile(markerPath, append(data, '\n'), 0o600); err != nil {
		return BindingResult{}, err
	}

	tmuxStarted, err := startTmuxSession(dockerBinary, tmuxBinary, accountPath, payload)
	if err != nil {
		return BindingResult{}, err
	}
	return BindingResult{
		Status:            "waiting_user_login",
		BindingSessionID:  payload.BindingID,
		ToolAccountID:     payload.ToolAccountID,
		ToolType:          payload.ToolType,
		AccountRemotePath: accountPath,
		TmuxSessionName:   payload.TmuxSessionName,
		ContainerName:     containerName(payload.ToolAccountID),
		MarkerPath:        markerPath,
		TmuxStarted:       tmuxStarted,
		Verifier:          payload.Verifier,
	}, nil
}

// ImportConfig writes local CLI config files into the remote tool account directory.
func ImportConfig(root string, payload ImportConfigPayload) (ImportConfigResult, error) {
	accountPath, err := resolveAccountPath(root, payload.UserID, payload.ToolType, payload.ToolAccountID, payload.AccountRemotePath)
	if err != nil {
		return ImportConfigResult{}, err
	}
	if err := os.MkdirAll(filepath.Join(accountPath, ".claude"), 0o700); err != nil {
		return ImportConfigResult{}, err
	}
	filesWritten := make([]string, 0, len(payload.Files))
	for _, file := range payload.Files {
		targetPath, err := resolveImportConfigTarget(accountPath, file.Path)
		if err != nil {
			return ImportConfigResult{}, err
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return ImportConfigResult{}, fmt.Errorf("decode %s: %w", file.Path, err)
		}
		mode := sanitizeFileMode(file.Mode)
		if err := writeFileAtomic(targetPath, content, mode); err != nil {
			return ImportConfigResult{}, err
		}
		filesWritten = append(filesWritten, file.Path)
	}
	return ImportConfigResult{
		Status:            "imported",
		ToolAccountID:     payload.ToolAccountID,
		ToolType:          payload.ToolType,
		AccountRemotePath: accountPath,
		FilesWritten:      filesWritten,
	}, nil
}

// Verify checks whether a tool account has enough remote auth state to be active.
func Verify(root string, payload VerifyPayload) (VerifyResult, error) {
	accountPath, err := resolveAccountPath(root, payload.UserID, payload.ToolType, payload.ToolAccountID, payload.AccountRemotePath)
	if err != nil {
		return VerifyResult{}, err
	}
	if payload.Verifier != "claude" || payload.ToolType != "claude" {
		return VerifyResult{}, fmt.Errorf("unsupported verifier %s for tool %s", payload.Verifier, payload.ToolType)
	}
	matches := existingClaudeAuthPaths(accountPath)
	result := VerifyResult{
		Verified:          len(matches) > 0,
		ToolAccountID:     payload.ToolAccountID,
		ToolType:          payload.ToolType,
		AccountRemotePath: accountPath,
		Metadata:          map[string]any{"matched_paths": matches},
	}
	if !result.Verified {
		result.Error = "Claude auth files were not found."
	}
	return result, nil
}

func startTmuxSession(dockerBinary string, tmuxBinary string, accountPath string, payload CreateBindingPayload) (bool, error) {
	if tmuxBinary == "" || payload.TmuxSessionName == "" {
		return false, nil
	}
	if _, err := exec.LookPath(tmuxBinary); err != nil {
		return false, nil
	}
	if err := ensureSandbox(dockerBinary, accountPath, payload); err != nil {
		return false, err
	}
	if err := exec.Command(tmuxBinary, "has-session", "-t", payload.TmuxSessionName).Run(); err == nil {
		return true, nil
	}
	cmd := exec.Command(tmuxBinary, "new-session", "-d", "-s", payload.TmuxSessionName, shellCommand(sandboxExecCommand(dockerBinary, accountPath, payload)))
	cmd.Dir = accountPath
	cmd.Env = append(os.Environ(),
		"AGENT_REMOTE_ACCOUNT_PATH="+accountPath,
		"AGENT_REMOTE_TOOL_TYPE="+payload.ToolType,
		"AGENT_REMOTE_REGION="+payload.RegionCode,
		"TZ="+payload.Timezone,
		"LANG="+payload.Locale,
		"LC_ALL="+payload.Locale,
	)
	if err := cmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

func ensureSandbox(dockerBinary string, accountPath string, payload CreateBindingPayload) error {
	if _, err := exec.LookPath(dockerBinary); err != nil {
		return err
	}
	cmd := exec.Command(dockerBinary, "sandbox", "create", "--name", containerName(payload.ToolAccountID), sandboxAgent(payload), accountPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "already exists") || strings.Contains(string(output), "already in use") {
			return nil
		}
		return fmt.Errorf("docker sandbox create failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func sandboxExecCommand(dockerBinary string, accountPath string, payload CreateBindingPayload) []string {
	command := payload.Template.Command
	if len(command) == 0 {
		command = []string{"claude"}
	}
	args := []string{
		dockerBinary,
		"sandbox",
		"exec",
		"-it",
		"-e", "CLAUDE_CONFIG_DIR=" + filepath.Join(accountPath, ".claude"),
		"-e", "TZ=" + payload.Timezone,
		"-e", "LANG=" + payload.Locale,
		"-e", "LC_ALL=" + payload.Locale,
		"-w", filepath.Join(accountPath, "workspace"),
		containerName(payload.ToolAccountID),
	}
	return append(args, command...)
}

func sandboxAgent(payload CreateBindingPayload) string {
	if payload.Template.SandboxAgent != "" {
		return payload.Template.SandboxAgent
	}
	return payload.ToolType
}

func containerName(toolAccountID string) string {
	replacer := strings.NewReplacer("-", "", "_", "")
	suffix := replacer.Replace(toolAccountID)
	if len(suffix) > 32 {
		suffix = suffix[:32]
	}
	return "agent-remote-bind-" + suffix
}

func ensureFile(path string, defaultContent []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, defaultContent, 0o600)
}

func resolveImportConfigTarget(accountPath string, importPath string) (string, error) {
	if importPath == "" || strings.Contains(importPath, "\\") || strings.Contains(importPath, "\x00") {
		return "", fmt.Errorf("invalid import path %q", importPath)
	}
	if !strings.HasPrefix(importPath, "~/.claude/") {
		return "", fmt.Errorf("unsupported import path %q", importPath)
	}
	relative := strings.TrimPrefix(importPath, "~/.claude/")
	parts := strings.Split(relative, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe import path %q", importPath)
		}
	}
	targetPath := filepath.Join(append([]string{accountPath, ".claude"}, parts...)...)
	if !isPathInside(filepath.Join(accountPath, ".claude"), targetPath) {
		return "", fmt.Errorf("import path %q resolves outside .claude", importPath)
	}
	return targetPath, nil
}

func sanitizeFileMode(mode uint32) os.FileMode {
	if mode == 0o644 {
		return 0o644
	}
	return 0o600
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-remote-import-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func shellCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '+' || r == ',' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func existingClaudeAuthPaths(accountPath string) []string {
	candidates := []string{
		".agent-remote-claude-auth.json",
		filepath.Join(".claude", ".credentials.json"),
		filepath.Join(".claude", "credentials.json"),
		".claude.json",
	}
	matches := make([]string, 0)
	for _, relativePath := range candidates {
		if hasAuthLikeFile(filepath.Join(accountPath, relativePath)) {
			matches = append(matches, relativePath)
		}
	}
	return matches
}

func hasAuthLikeFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Size() > 4
}

func resolveAccountPath(root string, userID string, toolType string, toolAccountID string, remotePath string) (string, error) {
	cleanRoot := filepath.Clean(root)
	accountPath := remotePath
	if accountPath == "" {
		accountPath = filepath.Join(cleanRoot, userID, "tool-accounts", toolType, toolAccountID)
	}
	cleanPath := filepath.Clean(accountPath)
	if mappedPath, ok := mapControlPlanePath(cleanRoot, cleanPath); ok {
		cleanPath = mappedPath
	}
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(cleanRoot, cleanPath)
	}
	if !isPathInside(cleanRoot, cleanPath) {
		return "", fmt.Errorf("account_remote_path %s is outside account_root %s", cleanPath, cleanRoot)
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
