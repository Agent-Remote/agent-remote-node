package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestSkillCapturePendingRequiresExactCommittedReceipt(t *testing.T) {
	for _, fault := range []string{"", "uncommitted", "wrong_status", "foreign_session", "rounded_generation", "missing", "alias", "null", "duplicate", "extra", "lost_response"} {
		t.Run(fault, func(t *testing.T) {
			capture, calls := skillmanager.CaptureFailure{Binding: terminationCapture().Binding, Code: "quota_exceeded"}, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/skill-snapshots/"+capture.Binding.SnapshotID+"/capture-pending" || r.Header.Get("Authorization") != "Bearer termination-test" {
					t.Error("termination lost authentication or identity")
				}
				body, _ := io.ReadAll(r.Body)
				var payload skillCapturePending
				if err := json.Unmarshal(body, &payload); err != nil || payload.TaskID != capture.Binding.TaskID || payload.DirectoryEpoch != capture.Binding.DirectoryEpoch || payload.CaptureError != capture.Code {
					t.Error("termination changed input", err)
				}
				committed, status, data := true, "stopped", string(body)
				switch fault {
				case "uncommitted":
					committed = false
				case "wrong_status":
					status = "persisted"
				case "foreign_session":
					data = strings.ReplaceAll(data, capture.Binding.SessionID, capture.Binding.AccountID)
				case "rounded_generation":
					data = strings.ReplaceAll(data, "9007199254740993", "9007199254740992")
				case "missing":
					data = strings.ReplaceAll(data, `,"unclean":false`, "")
				case "alias":
					data = strings.ReplaceAll(data, `"unclean"`, `"Unclean"`)
				case "null":
					data = strings.ReplaceAll(data, `"unclean":false`, `"unclean":null`)
				case "duplicate":
					data = strings.ReplaceAll(data, `"unclean":false`, `"unclean":false,"unclean":false`)
				case "extra":
					data = strings.ReplaceAll(data, `"unclean":false`, `"unclean":false,"path":"forbidden"`)
				case "lost_response":
					http.Error(w, "unavailable", 503)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": status, "committed": committed, "data": json.RawMessage(data)})
			}))
			defer server.Close()
			err := NewClient(server.URL, "termination-test").ObserveSkillCapturePending(context.Background(), capture)
			if (err != nil) != (fault != "") || calls != 1 {
				t.Fatal("unsafe receipt or automatic replay", err, calls)
			}
		})
	}
}
