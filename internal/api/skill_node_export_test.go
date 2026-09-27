package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func exportPermissionFixture() skillmanager.NodeExportPermission {
	b := terminationCapture().Binding
	return skillmanager.NodeExportPermission{
		Binding:  skillmanager.NodeExportBinding{SnapshotID: b.SnapshotID, SessionID: b.SessionID, UserID: b.UserID, AccountID: b.AccountID, NodeID: b.NodeID, TaskID: b.TaskID, LibraryGeneration: b.LibraryGeneration, DirectoryEpoch: b.DirectoryEpoch, InitialTreeDigest: b.InitialTreeDigest},
		DeviceID: "77777777-7777-4777-8777-777777777777", SSHKeyID: "88888888-8888-4888-8888-888888888888", ExpiresAt: time.Now().Add(time.Minute).UTC(), RecheckSeconds: 10,
	}
}

func TestFrozenExportHTTPRequiresExactCurrentPermission(t *testing.T) {
	for _, fault := range []string{"", "foreign_node", "foreign_device", "committed", "operation", "retryable", "extra", "alias", "missing", "duplicate", "null", "rounded_generation", "oversized", "redirect", "denied"} {
		t.Run(fault, func(t *testing.T) {
			permission := exportPermissionFixture()
			grant := "fixture=." + strings.Repeat("a", 64)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/skill-state-exports/"+permission.Binding.SnapshotID+"/verify" || r.Header.Get("Authorization") != "Bearer export-node-fixture" {
					t.Error("lost exact authenticated route")
				}
				body, _ := io.ReadAll(r.Body)
				var request map[string]string
				if json.Unmarshal(body, &request) != nil || len(request) != 3 || request["device_id"] != permission.DeviceID || request["ssh_key_id"] != permission.SSHKeyID || request["grant"] != grant {
					t.Error("changed exact original grant")
				}
				raw, _ := json.Marshal(map[string]any{"schema_version": 1, "operation_id": nil, "status": "authorized", "committed": false, "retryable": false, "data": permission, "errors": []string{}})
				data := string(raw)
				switch fault {
				case "foreign_node":
					data = strings.ReplaceAll(data, permission.Binding.NodeID, permission.Binding.UserID)
				case "foreign_device":
					data = strings.ReplaceAll(data, permission.DeviceID, permission.SSHKeyID)
				case "committed":
					data = strings.ReplaceAll(data, `"committed":false`, `"committed":true`)
				case "operation":
					data = strings.ReplaceAll(data, `"operation_id":null`, `"operation_id":"`+permission.Binding.TaskID+`"`)
				case "retryable":
					data = strings.ReplaceAll(data, `"retryable":false`, `"retryable":true`)
				case "extra":
					data = strings.ReplaceAll(data, `"recheck_seconds":10`, `"recheck_seconds":10,"path":"/etc/passwd"`)
				case "alias":
					data = strings.ReplaceAll(data, `"recheck_seconds"`, `"Recheck_seconds"`)
				case "missing":
					data = strings.ReplaceAll(data, `,"unclean":null`, ``)
				case "duplicate":
					data = strings.ReplaceAll(data, `"recheck_seconds":10`, `"recheck_seconds":10,"recheck_seconds":10`)
				case "null":
					data = strings.ReplaceAll(data, `"recheck_seconds":10`, `"recheck_seconds":null`)
				case "rounded_generation":
					data = strings.ReplaceAll(data, `9007199254740993`, `9007199254740993.0`)
				case "oversized":
					data += strings.Repeat(" ", 16385)
				case "redirect":
					w.Header().Set("Location", "/private")
					w.WriteHeader(302)
					return
				case "denied":
					w.WriteHeader(403)
					data = grant
				}
				_, _ = io.WriteString(w, data)
			}))
			defer server.Close()
			got, err := NewClient(server.URL, "export-node-fixture").VerifyNodeExport(context.Background(), permission.Binding.NodeID, permission.Binding.SnapshotID, permission.DeviceID, permission.SSHKeyID, grant)
			if (err != nil) != (fault != "") || calls != 1 {
				t.Fatal("unsafe permission or replay", fault, err, calls)
			}
			if err != nil && strings.Contains(err.Error(), grant) {
				t.Fatal("grant exposed")
			}
			if err == nil && got.Binding != permission.Binding {
				t.Fatal("generation rounded")
			}
		})
	}
}
