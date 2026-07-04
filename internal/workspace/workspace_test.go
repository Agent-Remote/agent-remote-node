package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCreatesWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()
	remotePath := filepath.Join(root, "user_1", "workspaces", "workspace_1", "files")
	result, err := Prepare(root, PreparePayload{
		UserID:        "user_1",
		WorkspaceID:   "workspace_1",
		SyncSessionID: "sync_1",
		RemotePath:    remotePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemotePath != remotePath {
		t.Fatalf("unexpected remote path: %s", result.RemotePath)
	}
	if _, err := os.Stat(result.MarkerPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.MarkerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "workspace_1") {
		t.Fatalf("marker missing workspace id: %s", string(data))
	}
}

func TestPrepareRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	_, err := Prepare(root, PreparePayload{
		UserID:        "user_1",
		WorkspaceID:   "workspace_1",
		SyncSessionID: "sync_1",
		RemotePath:    filepath.Join(t.TempDir(), "outside"),
	})
	if err == nil {
		t.Fatal("expected outside root error")
	}
}
