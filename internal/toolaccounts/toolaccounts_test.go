package toolaccounts

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeCreateBindingPayloadPreservesRuntimePolicy(t *testing.T) {
	payload := map[string]any{
		"binding_id": "bind_1", "tool_account_id": "account_1", "tool_type": "claude", "user_id": "user_1",
		"runtime_policy": map[string]any{"memory_max_bytes": float64(768 << 20)},
	}
	decoded, err := DecodeCreateBindingPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	policy, ok := roundTrip["runtime_policy"].(map[string]any)
	if !ok || policy["memory_max_bytes"] != float64(768<<20) {
		t.Fatalf("runtime policy was not preserved: %#v", roundTrip)
	}
}

func TestPrepareBindingCreatesAccountArchive(t *testing.T) {
	root := t.TempDir()
	payload := CreateBindingPayload{
		BindingID:         "bind_1",
		ToolAccountID:     "account_1",
		ToolType:          "claude",
		UserID:            "user_1",
		RegionCode:        "US",
		Timezone:          "America/Los_Angeles",
		Locale:            "en_US.UTF-8",
		AccountRemotePath: filepath.Join(root, "user_1", "accounts", "account_1"),
		TmuxSessionName:   "bind-claude",
		Template: RuntimeTemplate{
			SandboxAgent: "claude",
			Command:      []string{"claude", "login"},
			Verifier:     "claude",
		},
		Verifier: "claude",
	}

	result, err := PrepareBinding(root, "docker", "agent-remote-missing-tmux", payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "waiting_user_login" {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	if result.TmuxStarted {
		t.Fatal("tmux should be skipped when binary is missing")
	}
	for _, relativePath := range []string{".claude", ".claude.json", "cache", "workspace", ".agent-remote-tool-account.json"} {
		if _, err := os.Stat(filepath.Join(result.AccountRemotePath, relativePath)); err != nil {
			t.Fatalf("expected %s to exist: %v", relativePath, err)
		}
	}
	command := sandboxExecCommand("docker", result.AccountRemotePath, payload)
	assertContains(t, command, "sandbox")
	assertContains(t, command, "exec")
	assertContains(t, command, "CLAUDE_CONFIG_DIR="+filepath.Join(result.AccountRemotePath, ".claude"))
	assertContains(t, command, "-w")
	assertContains(t, command, filepath.Join(result.AccountRemotePath, "workspace"))
	assertContains(t, command, "agent-remote-bind-account1")
	assertContains(t, command, "claude")
	assertContains(t, command, "login")
}

func TestPrepareBindingRejectsOutsidePath(t *testing.T) {
	root := t.TempDir()
	_, err := PrepareBinding(root, "docker", "", CreateBindingPayload{
		BindingID:         "bind_1",
		ToolAccountID:     "account_1",
		ToolType:          "claude",
		UserID:            "user_1",
		AccountRemotePath: filepath.Join(filepath.Dir(root), "outside"),
	})
	if err == nil {
		t.Fatal("expected outside path to be rejected")
	}
}

func TestImportConfigWritesClaudeFiles(t *testing.T) {
	root := t.TempDir()
	result, err := ImportConfig(root, ImportConfigPayload{
		ToolAccountID: "account_1",
		ToolType:      "claude",
		UserID:        "user_1",
		Files: []ImportConfigFile{{
			Path:          "~/.claude/settings.json",
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("{\"theme\":\"dark\"}\n")),
			Mode:          0o600,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "imported" {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	content, err := os.ReadFile(filepath.Join(result.AccountRemotePath, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "{\"theme\":\"dark\"}\n" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func TestImportConfigRejectsUnsafePath(t *testing.T) {
	root := t.TempDir()
	_, err := ImportConfig(root, ImportConfigPayload{
		ToolAccountID: "account_1",
		ToolType:      "claude",
		UserID:        "user_1",
		Files: []ImportConfigFile{{
			Path:          "~/.claude/../credentials.json",
			ContentBase64: base64.StdEncoding.EncodeToString([]byte("{}\n")),
			Mode:          0o600,
		}},
	})
	if err == nil {
		t.Fatal("expected unsafe path to be rejected")
	}
}

func TestClaudeVerifierMatchesAuthFiles(t *testing.T) {
	root := t.TempDir()
	accountPath := filepath.Join(root, "user_1", "accounts", "account_1")
	if err := os.MkdirAll(filepath.Join(accountPath, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountPath, ".claude", "credentials.json"), []byte("{\"token\":\"test\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Verify(root, VerifyPayload{
		ToolAccountID:     "account_1",
		ToolType:          "claude",
		UserID:            "user_1",
		Verifier:          "claude",
		AccountRemotePath: accountPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatal("expected Claude verifier to succeed")
	}
	matches, ok := result.Metadata["matched_paths"].([]string)
	if !ok || len(matches) != 1 || matches[0] != ".claude/credentials.json" {
		t.Fatalf("unexpected matches: %#v", result.Metadata["matched_paths"])
	}
}

func TestClaudeVerifierReportsAuthPathInspectionErrors(t *testing.T) {
	root := t.TempDir()
	accountPath := filepath.Join(root, "user_1", "accounts", "account_1")
	configPath := filepath.Join(accountPath, ".claude")
	if err := os.MkdirAll(configPath, 0o700); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(configPath, ".credentials.json")
	if err := os.Symlink(".credentials.json", credentialPath); err != nil {
		t.Fatal(err)
	}

	_, err := Verify(root, VerifyPayload{
		ToolAccountID:     "account_1",
		ToolType:          "claude",
		UserID:            "user_1",
		Verifier:          "claude",
		AccountRemotePath: accountPath,
	})
	if err == nil || !strings.Contains(err.Error(), "inspect Claude auth path .claude/.credentials.json") {
		t.Fatalf("expected auth inspection error, got %v", err)
	}
}

func assertContains(t *testing.T, values []string, expected string) {
	t.Helper()
	for _, value := range values {
		if value == expected {
			return
		}
	}
	t.Fatalf("expected %q in %#v", expected, values)
}
