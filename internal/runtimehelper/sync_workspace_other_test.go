//go:build !linux

package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncWorkspaceUnsupportedHostMakesNoChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	engine := NewEngine(EngineConfig{WorkspaceRoot: root})
	_, err := engine.prepareWorkspace(context.Background(), map[string]any{"user_id": "user", "workspace_id": "workspace", "sync_session_id": "sync"})
	if err == nil {
		t.Fatal("unsupported host reported prepared")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("unsupported preparation changed filesystem: %v", err)
	}
}
