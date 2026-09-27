package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
)

func TestRuntimeRecoveryLeaseRejectsChangedAndUncertainReplies(t *testing.T) {
	account := "11111111-1111-4111-8111-111111111111"
	other := "22222222-2222-4222-8222-222222222222"
	binding := accountmigration.Binding{Version: 1, TaskID: "recover_tool_account_runtime:" + account + ":" + other, OriginalTaskID: "migrate_tool_account_runtime:" + account + ":" + other,
		TaskRecordID: account, OriginalTaskRecordID: other, NodeID: account, UserID: account, AccountID: account, ToolType: "claude", Source: "native", Target: "docker_sandbox"}
	grant := accountmigration.Authorization{Binding: binding, LeaseAttempt: 1}
	for _, scenario := range []string{"valid", "attempt", "original", "expired", "oversized", "interval", "missing-time", "redirect", "server-error", "empty", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var actual accountmigration.Authorization
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer fixture" || !strings.HasSuffix(r.URL.Path, "/runtime-migration-recovery-lease") || json.NewDecoder(r.Body).Decode(&actual) != nil || actual != grant {
					t.Error("lease request changed authority")
				}
				if scenario == "redirect" {
					w.Header().Set("Location", "/redirected")
					w.WriteHeader(307)
					return
				}
				if scenario == "server-error" {
					w.WriteHeader(503)
					return
				}
				if scenario == "empty" {
					return
				}
				now := time.Now().UTC()
				lease := RuntimeRecoveryLease{Authorization: grant, ServerTime: now, LeaseUntil: now.Add(30 * time.Second), RenewAfterMilliseconds: 10000}
				switch scenario {
				case "attempt":
					lease.Authorization.LeaseAttempt++
				case "original":
					lease.Authorization.Binding.OriginalTaskRecordID = account
				case "expired":
					lease.LeaseUntil = now
				case "oversized":
					lease.LeaseUntil = now.Add(301 * time.Second)
				case "interval":
					lease.RenewAfterMilliseconds = 30000
				case "missing-time":
					lease.ServerTime = time.Time{}
				}
				data, _ := json.Marshal(map[string]any{"data": lease})
				if scenario == "duplicate" {
					data = []byte(strings.Replace(string(data), `"lease_attempt":1`, `"lease_attempt":1,"lease_attempt":1`, 1))
				}
				_, _ = w.Write(data)
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "fixture").RenewRuntimeRecoveryLease(context.Background(), grant)
			if (err == nil) != (scenario == "valid") || calls != 1 {
				t.Fatal("invalid lease accepted or uncertain request replayed", err, calls)
			}
		})
	}
}
