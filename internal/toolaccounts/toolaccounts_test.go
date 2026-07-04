package toolaccounts

import (
	"os"
	"path/filepath"
	"testing"
)

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

func assertContains(t *testing.T, values []string, expected string) {
	t.Helper()
	for _, value := range values {
		if value == expected {
			return
		}
	}
	t.Fatalf("expected %q in %#v", expected, values)
}
