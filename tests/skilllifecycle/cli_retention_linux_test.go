package skilllifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// The host issues real CLI deletes concurrently while upload admission remains unavailable.
// This independent root client proves even the privileged cleanup boundary retains the input.
func checkPendingCLICleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	const requestPath = "/proof/control/pending-retention.json"
	const resultPath = "/proof/control/pending-retention-checked"
	if _, err := os.Stat(resultPath); err == nil {
		return
	}
	data, err := os.ReadFile(requestPath)
	if os.IsNotExist(err) {
		return
	}
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err != nil || json.Unmarshal(data, &request) != nil || len(request.SessionID) != 36 || filepath.Base(request.SessionID) != request.SessionID {
		t.Fatal("invalid pending-retention coordination identity")
	}
	bundle := filepath.Join(skillRoot, "session-"+request.SessionID)
	data, err = os.ReadFile(filepath.Join(bundle, "finalization/record.json"))
	var capture skillmanager.FinalizationRecord
	if err != nil || json.Unmarshal(data, &capture) != nil || capture.Validate() != nil || capture.Binding.SessionID != request.SessionID || capture.CanDeleteSession() || capture.Unclean {
		t.Fatal("cleanup proof requires the original clean, unacknowledged capture")
	}
	work := exportInventory(t, filepath.Join(bundle, "work"))
	objects := exportInventory(t, filepath.Join(bundle, "finalization/objects"))
	client := runtimehelper.NewClient("/run/agent-remote/runtime.sock")
	_, err = client.Call(ctx, "cli-pending-cleanup", "cleanup_resources", map[string]any{
		"runtime_backend": "native", "session_ids": []any{request.SessionID},
	})
	if err == nil || !strings.Contains(err.Error(), "state_pending") {
		t.Fatal("privileged cleanup did not refuse unretained state", err)
	}
	if !reflect.DeepEqual(work, exportInventory(t, filepath.Join(bundle, "work"))) ||
		!reflect.DeepEqual(objects, exportInventory(t, filepath.Join(bundle, "finalization/objects"))) {
		t.Fatal("pending cleanup changed original runtime or frozen bytes/metadata")
	}
	if err := os.WriteFile(resultPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
}
