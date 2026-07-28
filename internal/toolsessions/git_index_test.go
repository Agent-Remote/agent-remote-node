package toolsessions

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGitReadyInitializesIndexFromHead(t *testing.T) {
	workspacePath := t.TempDir()
	runGitForTest(t, workspacePath, "init")
	if err := os.WriteFile(filepath.Join(workspacePath, "tracked.txt"), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, workspacePath, "add", "tracked.txt")
	runGitForTest(t, workspacePath, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	indexPath := filepath.Join(workspacePath, ".git", "index")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitReady(workspacePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatal(err)
	}
	if output := runGitForTest(t, workspacePath, "status", "--porcelain"); strings.TrimSpace(output) != "" {
		t.Fatalf("rebuilt index must match HEAD: %s", output)
	}
}

func runGitForTest(t *testing.T, workspacePath string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", workspacePath}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return string(output)
}
