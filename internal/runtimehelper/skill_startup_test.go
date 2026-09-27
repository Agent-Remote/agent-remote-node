package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

func TestManagedSkillStartupRejectsBeforeLegacyStateAndCache(t *testing.T) {
	for _, operation := range []string{"start_session", "docker_start_session"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			engine := NewEngine(EngineConfig{StateRoot: filepath.Join(root, "runtime"), AccountRoot: filepath.Join(root, "accounts"), WorkspaceRoot: filepath.Join(root, "workspaces"), SkillStateRoot: filepath.Join(root, "skills")})
			request := Request{Version: ProtocolVersion, RequestID: "managed_start", Operation: operation, Payload: map[string]any{"skill_manager": nil}}
			_, err := engine.Execute(context.Background(), request)
			if !errors.Is(err, toolsessions.ErrManagedSkillsUnsupported) || classifyError(err) != "SKILL_MANAGER_UNSUPPORTED" {
				t.Fatalf("managed request reached legacy execution: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unsupported startup mutated runtime roots: %v, %v", entries, err)
			}
			if err := engine.saveResult(request.RequestID, map[string]any{"status": "running"}); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Execute(context.Background(), request); !errors.Is(err, toolsessions.ErrManagedSkillsUnsupported) {
				t.Fatalf("legacy cache authorized managed startup: %v", err)
			}
			if cached, ok, err := engine.cachedResult(request.RequestID); err != nil || !ok || cached["status"] != "running" {
				t.Fatalf("rejection changed historical receipt: %v, %v", cached, err)
			}
		})
	}
}
