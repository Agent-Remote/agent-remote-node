package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestExportRenewalPreservesPredecessorAndForcedCommandIdentity(t *testing.T) {
	for _, fault := range []string{"", "predecessor", "grant", "null", "extra", "alias", "duplicate", "node", "device", "committed", "operation", "denied", "redirect", "oversized", "eof"} {
		t.Run(fault, func(t *testing.T) {
			permission := exportPermissionFixture()
			previous := "previous=." + strings.Repeat("a", 64)
			successor := "successor=." + strings.Repeat("b", 64)
			digest := sha256.Sum256([]byte(previous))
			renewal := skillmanager.NodeExportRenewal{PreviousGrantDigest: hex.EncodeToString(digest[:]), Grant: successor, Permission: permission}
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/skill-state-exports/"+permission.Binding.SnapshotID+"/renew" || r.Header.Get("Authorization") != "Bearer export-node-fixture" || !r.Close {
					t.Error("renewal lost exact authenticated route or independent connection")
				}
				var request map[string]string
				if json.NewDecoder(r.Body).Decode(&request) != nil || len(request) != 3 || request["grant"] != previous || request["device_id"] != permission.DeviceID || request["ssh_key_id"] != permission.SSHKeyID {
					t.Error("renewal changed predecessor or forced-command identity")
				}
				raw, _ := json.Marshal(map[string]any{"schema_version": 1, "operation_id": nil, "status": "authorized", "committed": false, "retryable": false, "data": renewal, "errors": []string{}})
				data := string(raw)
				switch fault {
				case "predecessor":
					data = strings.ReplaceAll(data, renewal.PreviousGrantDigest, strings.Repeat("c", 64))
				case "grant":
					data = strings.ReplaceAll(data, successor, "invalid")
				case "null":
					data = strings.ReplaceAll(data, `"grant":"`+successor+`"`, `"grant":null`)
				case "extra":
					data = strings.ReplaceAll(data, `"previous_grant_digest":`, `"path":"/private","previous_grant_digest":`)
				case "alias":
					data = strings.ReplaceAll(data, `"grant":`, `"Grant":`)
				case "duplicate":
					data = strings.ReplaceAll(data, `"grant":`, `"grant":"`+successor+`","grant":`)
				case "node":
					data = strings.ReplaceAll(data, permission.Binding.NodeID, permission.Binding.UserID)
				case "device":
					data = strings.ReplaceAll(data, permission.DeviceID, permission.SSHKeyID)
				case "committed":
					data = strings.ReplaceAll(data, `"committed":false`, `"committed":true`)
				case "operation":
					data = strings.ReplaceAll(data, `"operation_id":null`, `"operation_id":"`+permission.Binding.TaskID+`"`)
				case "denied":
					w.WriteHeader(http.StatusForbidden)
					data = previous + successor
				case "redirect":
					w.Header().Set("Location", "/private")
					w.WriteHeader(http.StatusFound)
					return
				case "oversized":
					data += strings.Repeat(" ", 16385)
				case "eof":
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error("could not close uncertain response")
						return
					}
					_ = connection.Close()
					return
				}
				_, _ = io.WriteString(w, data)
			}))
			defer server.Close()
			got, err := NewClient(server.URL, "export-node-fixture").RenewNodeExport(context.Background(), permission.Binding.NodeID, permission.Binding.SnapshotID, permission.DeviceID, permission.SSHKeyID, previous)
			if (err != nil) != (fault != "") || calls.Load() != 1 {
				t.Fatal("unsafe continuation or request replay", fault, calls.Load())
			}
			if err != nil {
				if strings.Contains(err.Error(), previous) || strings.Contains(err.Error(), successor) || got != (skillmanager.NodeExportRenewal{}) {
					t.Fatal("failed continuation exposed credential data")
				}
			} else if got != renewal {
				t.Fatal("continuation changed exact permission or credentials")
			}
		})
	}
}

func TestExportRenewalRejectsMalformedCredentialsBeforeNetwork(t *testing.T) {
	permission := exportPermissionFixture()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("malformed credential reached HTTP")
	}))
	defer server.Close()
	for _, grant := range []string{"", "invalid", "fixture=." + strings.Repeat("a", 4096), "fixture=." + strings.Repeat("a", 64) + "\n"} {
		if _, err := NewClient(server.URL, "export-node-fixture").RenewNodeExport(context.Background(), permission.Binding.NodeID, permission.Binding.SnapshotID, permission.DeviceID, permission.SSHKeyID, grant); err == nil {
			t.Fatal("malformed credential accepted")
		}
	}
}
