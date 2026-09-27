package toolaccounts

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportPreflightsWholeBatchBeforeAnyWrite(t *testing.T) {
	for _, mode := range []string{"managed_v1", "migrating", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			_, err := ImportConfig(root, importPayload("~/.claude/skills/demo/SKILL.md"), mode)
			if err == nil || (mode != "unknown" && !errors.Is(err, ErrSkillManagerOwnsPath)) {
				t.Fatalf("expected ownership rejection, got %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected batch changed account root: %v %v", entries, err)
			}
		})
	}
}

func TestImportPreflightRejectsLateInvalidContentAndPath(t *testing.T) {
	for _, invalid := range []string{"base64", "path", "size"} {
		t.Run(invalid, func(t *testing.T) {
			root := t.TempDir()
			payload := importPayload("~/.claude/agents/demo.md")
			switch invalid {
			case "base64":
				payload.Files[1].ContentBase64 = "invalid!"
			case "path":
				payload.Files[1].Path = "~/.claude/../outside"
			case "size":
				payload.Files[1].ContentBase64 = strings.Repeat("A", base64.StdEncoding.EncodedLen(maxImportFileBytes)+4)
			}
			if _, err := ImportConfig(root, payload, "legacy"); err == nil {
				t.Fatal("expected preflight rejection")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("partial import before rejection: %v %v", entries, err)
			}
		})
	}
}

func TestImportKeepsLegacySkillsAndManagedNonSkillConfiguration(t *testing.T) {
	for _, mode := range []string{"legacy", "managed_v1", "migrating"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := "~/.claude/plugins/demo/skills/SKILL.md"
			if mode == "legacy" {
				path = "~/.claude/skills/demo/SKILL.md"
			}
			result, err := ImportConfig(root, importPayload(path), mode)
			if err != nil || len(result.FilesWritten) != 2 {
				t.Fatalf("valid batch failed: %#v %v", result, err)
			}
			content, err := os.ReadFile(filepath.Join(result.AccountRemotePath, ".claude", "settings.json"))
			if err != nil || string(content) != "{}" {
				t.Fatalf("configuration was not written: %q %v", content, err)
			}
		})
	}
}

func importPayload(lastPath string) ImportConfigPayload {
	return ImportConfigPayload{
		ToolAccountID: "account_1", ToolType: "claude", UserID: "user_1",
		Files: []ImportConfigFile{
			{Path: "~/.claude/settings.json", ContentBase64: "e30=", Mode: 0o600},
			{Path: lastPath, ContentBase64: "YQ==", Mode: 0o600},
		},
	}
}

func TestImportRejectsSymlinkAliasesBeforeWritingOtherFiles(t *testing.T) {
	for _, position := range []string{"user", "account", "config", "ancestor", "leaf"} {
		t.Run(position, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			account := filepath.Join(root, "user_1", "tool-accounts", "claude", "account_1")
			claude := filepath.Join(account, ".claude")
			if err := os.MkdirAll(claude, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "protected"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := ""
			payload := importPayload("~/.claude/agents/protected")
			switch position {
			case "user":
				link = filepath.Join(root, "user_1")
			case "account":
				link = account
			case "config":
				link = claude
			case "ancestor":
				link = filepath.Join(claude, "agents")
			case "leaf":
				if err := os.Mkdir(filepath.Join(claude, "agents"), 0o700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(claude, "agents", "protected")
			}
			if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			target := outside
			if position == "leaf" {
				target = filepath.Join(outside, "protected")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := ImportConfig(root, payload, "managed_v1"); err == nil {
				t.Fatal("symlink alias accepted")
			}
			if _, err := os.Stat(filepath.Join(claude, "settings.json")); !os.IsNotExist(err) {
				t.Fatalf("earlier file was written: %v", err)
			}
			content, err := os.ReadFile(filepath.Join(outside, "protected"))
			if err != nil || string(content) != "original" {
				t.Fatalf("alias target changed: %q %v", content, err)
			}
		})
	}
}

func TestImportRejectsNonSkillAliasIntoAccountSkills(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, "user_1", "tool-accounts", "claude", "account_1", ".claude")
	if err := os.MkdirAll(filepath.Join(claude, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "skills", "protected"), []byte("learned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("skills", filepath.Join(claude, "agents")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportConfig(root, importPayload("~/.claude/agents/protected"), "managed_v1"); err == nil {
		t.Fatal("non-skill alias into skills was accepted")
	}
	content, err := os.ReadFile(filepath.Join(claude, "skills", "protected"))
	if err != nil || string(content) != "learned" {
		t.Fatalf("skill state changed: %q %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(claude, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("partial configuration import: %v", err)
	}
}

func TestImportAllowsConfiguredRootAliasAndReplacesFileWithoutFollowingHardlink(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "configured-root")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	first, err := ImportConfig(alias, importPayload("~/.claude/rules/demo.md"), "managed_v1")
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(first.AccountRemotePath, ".claude", "settings.json")
	retained := filepath.Join(root, "retained-settings")
	if err := os.Link(settings, retained); err != nil {
		t.Fatal(err)
	}
	payload := importPayload("~/.claude/rules/demo.md")
	payload.Files[0].ContentBase64 = "bmV3"
	if _, err := ImportConfig(alias, payload, "managed_v1"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(retained)
	if err != nil || string(content) != "{}" {
		t.Fatalf("shared inode was modified: %q %v", content, err)
	}
	content, err = os.ReadFile(settings)
	if err != nil || string(content) != "new" {
		t.Fatalf("import did not replace target: %q %v", content, err)
	}
}

func TestImportRejectsBatchPathCollisionsBeforeAnyWrite(t *testing.T) {
	for _, last := range []string{"~/.claude/settings.json", "~/.claude/settings.json/child"} {
		t.Run(last, func(t *testing.T) {
			root := t.TempDir()
			if _, err := ImportConfig(root, importPayload(last), "managed_v1"); err == nil {
				t.Fatal("colliding batch accepted")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("colliding batch changed root: %v %v", entries, err)
			}
		})
	}
}
