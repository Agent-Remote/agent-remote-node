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

func terminationCapture() skillmanager.FinalizationRecord {
	return skillmanager.FinalizationRecord{Version: 1, ObjectsVersion: 1, State: "local_durable", TreeDigest: strings.Repeat("b", 64),
		Binding: skillmanager.SnapshotBinding{NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", AccountID: "33333333-3333-4333-8333-333333333333",
			SessionID: "44444444-4444-4444-8444-444444444444", SnapshotID: "55555555-5555-4555-8555-555555555555", TaskID: "66666666-6666-4666-8666-666666666666", PreparationDigest: strings.Repeat("c", 64),
			InitialTreeDigest: strings.Repeat("a", 64), DirectoryEpoch: 9007199254740993, LibraryGeneration: 9007199254740995}}
}

func TestSkillTerminationRequiresExactCommittedReceipt(t *testing.T) {
	for _, fault := range []string{"", "uncommitted", "wrong_status", "foreign_session", "rounded_generation", "missing", "alias", "null", "duplicate", "extra", "lost_response"} {
		t.Run(fault, func(t *testing.T) {
			capture, calls := terminationCapture(), 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/skill-snapshots/"+capture.Binding.SnapshotID+"/termination" || r.Header.Get("Authorization") != "Bearer termination-test" {
					t.Error("termination lost authentication or identity")
				}
				body, _ := io.ReadAll(r.Body)
				var payload skillTermination
				if err := json.Unmarshal(body, &payload); err != nil || payload.TaskID != capture.Binding.TaskID || payload.DirectoryEpoch != capture.Binding.DirectoryEpoch || payload.IncomingDigest != capture.TreeDigest {
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
			err := NewClient(server.URL, "termination-test").ObserveSkillTermination(context.Background(), capture)
			if (err != nil) != (fault != "") || calls != 1 {
				t.Fatal("unsafe receipt or automatic replay", err, calls)
			}
		})
	}
}
