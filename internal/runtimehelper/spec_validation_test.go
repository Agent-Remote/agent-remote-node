package runtimehelper

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBuildSpecRejectsInvalidConfigurationBeforeRuntimeMutation(t *testing.T) {
	locale := writeTestCommand(t, "locale", "printf 'C\\nen_US.utf8\\n'")
	t.Setenv("PATH", filepath.Dir(locale)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, test := range []struct {
		name, field, value, message string
	}{
		{"locale", "locale", "en-US", "timezone or locale is invalid"},
		{"unavailable locale", "locale", "zz_ZZ.UTF-8", "timezone or locale is invalid"},
		{"timezone", "timezone", "../UTC", "timezone or locale is invalid"},
		{"tmux name", "tmux_session_name", "../tmux", "tmux_session_name"},
	} {
		for _, existing := range []bool{false, true} {
			t.Run(test.name+"/existing="+strconv.FormatBool(existing), func(t *testing.T) {
				root := t.TempDir()
				sessionRoot := filepath.Join(root, "sessions", "binding-test")
				specPath := filepath.Join(sessionRoot, "spec.json")
				if existing {
					if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(specPath, []byte("original evidence"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				payload := map[string]any{"locale": "C", test.field: test.value}
				engine := NewEngine(EngineConfig{StateRoot: root})
				_, err := engine.buildSpec(payload, "binding-test", "validation-test", "account-test", "", "", nil, "binding")
				if err == nil || !strings.Contains(err.Error(), test.message) {
					t.Fatalf("expected configuration rejection before identity provisioning, got %v", err)
				}
				if existing {
					content, err := os.ReadFile(specPath)
					if err != nil || string(content) != "original evidence" {
						t.Fatalf("existing evidence changed: %q, %v", content, err)
					}
					entries, err := os.ReadDir(sessionRoot)
					if err != nil || len(entries) != 1 {
						t.Fatalf("invalid configuration changed runtime directory: %v, %v", entries, err)
					}
				} else if _, err := os.Stat(sessionRoot); !os.IsNotExist(err) {
					t.Fatalf("invalid configuration left a runtime directory: %v", err)
				}
			})
		}
	}
}
