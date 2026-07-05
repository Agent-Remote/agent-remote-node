package toolsessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RuntimeTemplate describes the command used by a tool session.
type RuntimeTemplate struct {
	SandboxAgent string   `json:"sandbox_agent"`
	Command      []string `json:"command"`
	Verifier     string   `json:"verifier"`
}

// CreatePayload describes a create_tool_session task payload.
type CreatePayload struct {
	SessionID           string          `json:"session_id"`
	ToolAccountID       string          `json:"tool_account_id"`
	ToolType            string          `json:"tool_type"`
	UserID              string          `json:"user_id"`
	WorkspaceID         string          `json:"workspace_id"`
	ProjectKey          string          `json:"project_key"`
	WorkspaceRemotePath string          `json:"workspace_remote_path"`
	AccountRemotePath   string          `json:"account_remote_path"`
	TmuxSessionName     string          `json:"tmux_session_name"`
	SandboxName         string          `json:"sandbox_name"`
	Timezone            string          `json:"timezone"`
	Locale              string          `json:"locale"`
	Argv                []string        `json:"argv"`
	Template            RuntimeTemplate `json:"template"`
}

// CreateResult describes the prepared tool session.
type CreateResult struct {
	Status              string `json:"status"`
	SessionID           string `json:"session_id"`
	ToolAccountID       string `json:"tool_account_id"`
	ToolType            string `json:"tool_type"`
	WorkspaceRemotePath string `json:"workspace_remote_path"`
	AccountRemotePath   string `json:"account_remote_path"`
	TmuxSessionName     string `json:"tmux_session_name"`
	SandboxName         string `json:"sandbox_name"`
	MarkerPath          string `json:"marker_path"`
	TmuxStarted         bool   `json:"tmux_started"`
}

// StopPayload describes a stop_tool_session task payload.
type StopPayload struct {
	SessionID       string `json:"session_id"`
	TmuxSessionName string `json:"tmux_session_name"`
	SandboxName     string `json:"sandbox_name"`
}

// StopResult describes stopped runtime resources.
type StopResult struct {
	Status          string `json:"status"`
	SessionID       string `json:"session_id"`
	TmuxSessionName string `json:"tmux_session_name"`
	SandboxName     string `json:"sandbox_name"`
	TmuxStopped     bool   `json:"tmux_stopped"`
	SandboxRemoved  bool   `json:"sandbox_removed"`
}

// DecodeCreatePayload converts a generic task payload into a typed payload.
func DecodeCreatePayload(payload map[string]any) (CreatePayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return CreatePayload{}, err
	}
	var decoded CreatePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return CreatePayload{}, err
	}
	if decoded.SessionID == "" {
		return CreatePayload{}, errors.New("session_id is required")
	}
	if decoded.ToolAccountID == "" {
		return CreatePayload{}, errors.New("tool_account_id is required")
	}
	if decoded.ToolType == "" {
		return CreatePayload{}, errors.New("tool_type is required")
	}
	if decoded.UserID == "" {
		return CreatePayload{}, errors.New("user_id is required")
	}
	if decoded.WorkspaceID == "" {
		return CreatePayload{}, errors.New("workspace_id is required")
	}
	if decoded.TmuxSessionName == "" {
		return CreatePayload{}, errors.New("tmux_session_name is required")
	}
	if decoded.SandboxName == "" {
		return CreatePayload{}, errors.New("sandbox_name is required")
	}
	return decoded, nil
}

// DecodeStopPayload converts a generic task payload into a typed payload.
func DecodeStopPayload(payload map[string]any) (StopPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return StopPayload{}, err
	}
	var decoded StopPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return StopPayload{}, err
	}
	if decoded.SessionID == "" {
		return StopPayload{}, errors.New("session_id is required")
	}
	return decoded, nil
}

// Prepare creates the workspace/account directories and starts a tmux-held sandbox exec.
func Prepare(workspaceRoot string, accountRoot string, dockerBinary string, tmuxBinary string, payload CreatePayload) (CreateResult, error) {
	workspacePath, err := resolvePath(workspaceRoot, payload.UserID, filepath.Join("workspaces", payload.WorkspaceID, "files"), payload.WorkspaceRemotePath, "workspace_remote_path")
	if err != nil {
		return CreateResult{}, err
	}
	accountPath, err := resolvePath(accountRoot, payload.UserID, filepath.Join("accounts", payload.ToolAccountID), payload.AccountRemotePath, "account_remote_path")
	if err != nil {
		return CreateResult{}, err
	}
	for _, dir := range []string{workspacePath, accountPath, filepath.Join(accountPath, ".claude")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return CreateResult{}, err
		}
	}
	if err := ensureFile(filepath.Join(accountPath, ".claude.json"), []byte("{}\n")); err != nil {
		return CreateResult{}, err
	}
	markerPath := filepath.Join(workspacePath, ".agent-remote-session.json")
	marker := map[string]any{
		"session_id":            payload.SessionID,
		"tool_account_id":       payload.ToolAccountID,
		"tool_type":             payload.ToolType,
		"user_id":               payload.UserID,
		"workspace_id":          payload.WorkspaceID,
		"project_key":           payload.ProjectKey,
		"workspace_remote_path": workspacePath,
		"account_remote_path":   accountPath,
		"tmux_session_name":     payload.TmuxSessionName,
		"sandbox_name":          payload.SandboxName,
		"timezone":              payload.Timezone,
		"locale":                payload.Locale,
		"command":               sessionCommand(payload),
		"prepared_at":           time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return CreateResult{}, err
	}
	if err := os.WriteFile(markerPath, append(data, '\n'), 0o600); err != nil {
		return CreateResult{}, err
	}

	tmuxStarted, err := startTmuxSession(dockerBinary, tmuxBinary, workspacePath, accountPath, payload)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{
		Status:              "running",
		SessionID:           payload.SessionID,
		ToolAccountID:       payload.ToolAccountID,
		ToolType:            payload.ToolType,
		WorkspaceRemotePath: workspacePath,
		AccountRemotePath:   accountPath,
		TmuxSessionName:     payload.TmuxSessionName,
		SandboxName:         payload.SandboxName,
		MarkerPath:          markerPath,
		TmuxStarted:         tmuxStarted,
	}, nil
}

// Stop terminates a tmux-held tool session and removes its sandbox when available.
func Stop(dockerBinary string, tmuxBinary string, payload StopPayload) (StopResult, error) {
	tmuxStopped := false
	if payload.TmuxSessionName != "" {
		if _, err := exec.LookPath(tmuxBinary); err == nil {
			cmd := exec.Command(tmuxBinary, "kill-session", "-t", payload.TmuxSessionName)
			if err := cmd.Run(); err == nil || isTmuxMissingSession(err) {
				tmuxStopped = true
			} else {
				return StopResult{}, err
			}
		}
	}
	sandboxRemoved := false
	if payload.SandboxName != "" {
		if _, err := exec.LookPath(dockerBinary); err == nil {
			cmd := exec.Command(dockerBinary, "sandbox", "rm", payload.SandboxName)
			if output, err := cmd.CombinedOutput(); err == nil || isSandboxMissing(string(output)) {
				sandboxRemoved = true
			} else {
				return StopResult{}, fmt.Errorf("docker sandbox rm failed: %w: %s", err, strings.TrimSpace(string(output)))
			}
		}
	}
	return StopResult{
		Status:          "stopped",
		SessionID:       payload.SessionID,
		TmuxSessionName: payload.TmuxSessionName,
		SandboxName:     payload.SandboxName,
		TmuxStopped:     tmuxStopped,
		SandboxRemoved:  sandboxRemoved,
	}, nil
}

func startTmuxSession(dockerBinary string, tmuxBinary string, workspacePath string, accountPath string, payload CreatePayload) (bool, error) {
	if tmuxBinary == "" || payload.TmuxSessionName == "" {
		return false, nil
	}
	if _, err := exec.LookPath(tmuxBinary); err != nil {
		return false, nil
	}
	if err := ensureSandbox(dockerBinary, workspacePath, accountPath, payload); err != nil {
		return false, err
	}
	if err := exec.Command(tmuxBinary, "has-session", "-t", payload.TmuxSessionName).Run(); err == nil {
		return true, nil
	}
	cmd := exec.Command(tmuxBinary, "new-session", "-d", "-s", payload.TmuxSessionName, shellCommand(sandboxExecCommand(dockerBinary, workspacePath, accountPath, payload)))
	cmd.Dir = workspacePath
	cmd.Env = append(os.Environ(),
		"AGENT_REMOTE_WORKSPACE_PATH="+workspacePath,
		"AGENT_REMOTE_ACCOUNT_PATH="+accountPath,
		"AGENT_REMOTE_TOOL_TYPE="+payload.ToolType,
		"TZ="+payload.Timezone,
		"LANG="+payload.Locale,
		"LC_ALL="+payload.Locale,
	)
	if err := cmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

func ensureSandbox(dockerBinary string, workspacePath string, accountPath string, payload CreatePayload) error {
	if _, err := exec.LookPath(dockerBinary); err != nil {
		return err
	}
	cmd := exec.Command(dockerBinary, "sandbox", "create", "--name", payload.SandboxName, sandboxAgent(payload), workspacePath, accountPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "already exists") || strings.Contains(string(output), "already in use") {
			return nil
		}
		return fmt.Errorf("docker sandbox create failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func sandboxExecCommand(dockerBinary string, workspacePath string, accountPath string, payload CreatePayload) []string {
	args := []string{
		dockerBinary,
		"sandbox",
		"exec",
		"-it",
		"-e", "CLAUDE_CONFIG_DIR=" + filepath.Join(accountPath, ".claude"),
		"-e", "TZ=" + payload.Timezone,
		"-e", "LANG=" + payload.Locale,
		"-e", "LC_ALL=" + payload.Locale,
		"-w", workspacePath,
		payload.SandboxName,
	}
	return append(args, sessionCommand(payload)...)
}

func sessionCommand(payload CreatePayload) []string {
	if len(payload.Template.Command) > 0 {
		return payload.Template.Command
	}
	if len(payload.Argv) > 0 {
		return append([]string{payload.ToolType}, payload.Argv...)
	}
	return []string{payload.ToolType}
}

func sandboxAgent(payload CreatePayload) string {
	if payload.Template.SandboxAgent != "" {
		return payload.Template.SandboxAgent
	}
	return payload.ToolType
}

func resolvePath(root string, userID string, defaultSuffix string, remotePath string, label string) (string, error) {
	cleanRoot := filepath.Clean(root)
	candidate := remotePath
	if candidate == "" {
		candidate = filepath.Join(cleanRoot, userID, defaultSuffix)
	}
	cleanPath := filepath.Clean(candidate)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(cleanRoot, cleanPath)
	}
	if !isPathInside(cleanRoot, cleanPath) {
		return "", fmt.Errorf("%s %s is outside root %s", label, cleanPath, cleanRoot)
	}
	return cleanPath, nil
}

func ensureFile(path string, defaultContent []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, defaultContent, 0o600)
}

func isPathInside(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(os.PathSeparator))
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

func isTmuxMissingSession(err error) bool {
	return err != nil
}

func isSandboxMissing(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "not found") || strings.Contains(lower, "no such")
}
