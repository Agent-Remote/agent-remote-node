package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

func TestManagedSkillStartupNeverCallsLegacyHelper(t *testing.T) {
	for _, backend := range []string{"native", "docker_sandbox"} {
		// No client or socket is configured: rejection must precede either authority boundary.
		worker := Worker{}
		task := api.TaskEnvelope{TaskType: "create_tool_session", Payload: map[string]any{
			"runtime_backend": backend, "skill_manager": map[string]any{"snapshot_id": "private-input"},
		}}
		if _, err := worker.executeKnownTask(context.Background(), task); !errors.Is(err, toolsessions.ErrManagedSkillsUnsupported) {
			t.Fatalf("managed task reached legacy dispatch: %v", err)
		}
		err := fmt.Errorf("private-input: %w", toolsessions.ErrManagedSkillsUnsupported)
		failure := contentSafeTaskError(task, err)
		if failure["code"] != "SKILL_MANAGER_UNSUPPORTED" || strings.Contains(failure["message"].(string), "private-input") {
			t.Fatalf("managed rejection lost its bounded code: %v", failure)
		}
	}
}
