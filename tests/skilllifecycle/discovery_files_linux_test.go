package skilllifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeDiscoveryFiles(t *testing.T) {
	for _, scenario := range []string{"valid", "missing", "unrelated", "symlink", "linked_parent", "directory", "duplicate", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			account := t.TempDir()
			project := filepath.Join(account, "projects", "workspace")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(project, discoveryRun+".jsonl")
			write := func(path string) {
				if err := os.WriteFile(path, []byte(discoveryUse+discoveryResult), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "valid":
				write(trace)
			case "missing":
			case "unrelated":
				write(filepath.Join(project, "unrelated.jsonl"))
			case "directory":
				if err := os.Mkdir(trace, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := filepath.Join(t.TempDir(), "trace.jsonl")
				write(other)
				if err := os.Symlink(other, trace); err != nil {
					t.Fatal(err)
				}
			case "linked_parent":
				other := t.TempDir()
				write(filepath.Join(other, discoveryRun+".jsonl"))
				if err := os.Symlink(other, filepath.Join(project, "linked")); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				write(trace)
				write(filepath.Join(account, "projects", discoveryRun+".jsonl"))
			case "oversized":
				write(trace)
				if err := os.Truncate(trace, claudeTraceBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			if (inspectClaudeDiscovery(account, discoveryRun) == nil) != (scenario == "valid") {
				t.Fatal("unexpected transcript selection outcome")
			}
		})
	}
}
