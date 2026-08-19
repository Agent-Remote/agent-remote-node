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
	maintenanceLock := filepath.Join(workspacePath, ".git", "objects", "maintenance.lock")
	if err := os.WriteFile(maintenanceLock, nil, 0o600); err != nil {
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

func TestEnsureGitReadyRejectsIndexLock(t *testing.T) {
	workspacePath := t.TempDir()
	runGitForTest(t, workspacePath, "init")
	indexLock := filepath.Join(workspacePath, ".git", "index.lock")
	if err := os.WriteFile(indexLock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitReady(workspacePath); err == nil || !strings.Contains(err.Error(), indexLock) {
		t.Fatalf("expected active index lock error, got %v", err)
	}
}

func runGitForTest(t *testing.T, workspacePath string, args ...string) string {
	t.Helper()
	base := []string{"-c", "maintenance.auto=false", "-c", "gc.auto=0", "-C", workspacePath}
	command := exec.Command("git", append(base, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return string(output)
}
