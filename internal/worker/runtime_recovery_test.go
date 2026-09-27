package worker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/accountmigration"
	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func recoveryBindingFixture() accountmigration.Binding {
	account := "11111111-1111-4111-8111-111111111111"
	return accountmigration.Binding{Version: 1, TaskID: "recover_tool_account_runtime:" + account + ":22222222-2222-4222-8222-222222222222", TaskRecordID: "33333333-3333-4333-8333-333333333333", OriginalTaskID: "migrate_tool_account_runtime:" + account + ":44444444-4444-4444-8444-444444444444", OriginalTaskRecordID: "55555555-5555-4555-8555-555555555555", NodeID: "66666666-6666-4666-8666-666666666666", UserID: "77777777-7777-4777-8777-777777777777", AccountID: account, ToolType: "claude", Source: "docker_sandbox", Target: "native"}
}

func TestRuntimeRecoveryFreshAuthorityAndHelperEvidenceBypassGenericLedger(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) { testRuntimeRecoveryVersion(t, version) })
	}
}

func testRuntimeRecoveryVersion(t *testing.T, version int) {
	for _, scenario := range []string{"success", "cached-success", "cached-failure", "missing-grant", "wrong-original", "wrong-attempt", "wrong-helper", "wrong-action-helper", "wrong-action-grant", "wrong-action-lease", "helper-failure", "wrong-poll", "wrong-lease", "lost-lease"} {
		t.Run(scenario, func(t *testing.T) {
			binding := recoveryBindingFixture()
			binding.Version = version
			if version == 2 {
				binding.Action = "verify_source"
			}
			if version == 3 {
				binding.Action = "repair_source"
			}
			authorization := accountmigration.Authorization{Binding: binding, LeaseAttempt: 1}
			var helperCalls, successes, failures, renewals atomic.Int32
			socket := filepath.Join(os.TempDir(), "ar-recover-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			defer os.Remove(socket)
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					var request runtimehelper.Request
					_ = json.NewDecoder(conn).Decode(&request)
					helperCalls.Add(1)
					if request.Operation != "recover_account_migration" || request.RequestID != binding.TaskID {
						t.Error("wrong helper recovery operation")
					}
					expected, _ := runtimehelper.Map(authorization)
					left, _ := json.Marshal(request.Payload)
					right, _ := json.Marshal(expected)
					if string(left) != string(right) {
						t.Error("helper received different authority")
					}
					response := runtimehelper.Response{Version: 1, OK: true, Result: map[string]any{"recovered": true, "authorization": authorization}}
					if scenario == "wrong-helper" {
						changed := authorization
						changed.Binding.OriginalTaskRecordID = binding.TaskRecordID
						response.Result["authorization"] = changed
					}
					if scenario == "wrong-action-helper" {
						changed := authorization
						changed.Binding = alternateRecoveryAction(changed.Binding)
						response.Result["authorization"] = changed
					}
					if scenario == "helper-failure" {
						response.OK = false
						response.Error = &runtimehelper.Error{Code: "STATE_MIGRATION_PENDING", Message: "secret private account content"}
					}
					if scenario == "lost-lease" {
						_, _ = io.Copy(io.Discard, conn)
						_ = conn.Close()
						continue
					}
					_ = json.NewEncoder(conn).Encode(response)
					_ = conn.Close()
				}
			}()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-node" {
					t.Error("missing node authentication")
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/start"):
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
				case strings.HasSuffix(r.URL.Path, "/runtime-migration-recovery-authorization"):
					grant := authorization
					if scenario == "missing-grant" {
						w.WriteHeader(404)
						return
					}
					if scenario == "wrong-original" {
						grant.Binding.OriginalTaskRecordID = binding.TaskRecordID
					}
					if scenario == "wrong-action-grant" {
						grant.Binding = alternateRecoveryAction(grant.Binding)
					}
					if scenario == "wrong-attempt" {
						grant.LeaseAttempt++
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": grant})
				case strings.HasSuffix(r.URL.Path, "/runtime-migration-recovery-lease"):
					count := renewals.Add(1)
					var expected accountmigration.Authorization
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&expected) != nil || expected != authorization {
						t.Error("changed lease request")
					}
					if scenario == "lost-lease" && count > 1 {
						w.WriteHeader(404)
						return
					}
					now := time.Now().UTC()
					lease := api.RuntimeRecoveryLease{Authorization: authorization, ServerTime: now, LeaseUntil: now.Add(time.Second), RenewAfterMilliseconds: 10}
					if scenario == "wrong-action-lease" {
						lease.Authorization.Binding = alternateRecoveryAction(lease.Authorization.Binding)
					}
					if scenario == "wrong-lease" {
						lease.Authorization.LeaseAttempt++
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": lease})
				case strings.HasSuffix(r.URL.Path, "/complete"):
					successes.Add(1)
					var value struct {
						Result map[string]any `json:"result"`
					}
					_ = json.NewDecoder(r.Body).Decode(&value)
					if !matchesRecoveryResult(value.Result, authorization) {
						t.Error("unbound recovery completion")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
				case strings.HasSuffix(r.URL.Path, "/fail"):
					failures.Add(1)
					var value struct {
						Error map[string]any `json:"error"`
					}
					_ = json.NewDecoder(r.Body).Decode(&value)
					data, _ := json.Marshal(value.Error)
					if strings.Contains(string(data), "secret") || value.Error["code"] != "RUNTIME_MIGRATION_RECOVERY_REQUIRED" || value.Error["lease_attempt"] != float64(1) {
						t.Error("failure disclosed content or lost authority")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
				default:
					t.Error("unexpected recovery route")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			journal, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.json"))
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "cached-success" || scenario == "cached-failure" {
				status := "succeeded"
				if scenario == "cached-failure" {
					status = "failed"
				}
				if err := journal.Save(ledger.Entry{TaskID: binding.TaskID, Status: status, Result: map[string]any{"cached": true}, Error: map[string]any{"cached": true}}); err != nil {
					t.Fatal(err)
				}
			}
			worker := New(config.Config{NodeID: binding.NodeID, RuntimeSocketPath: socket}, api.NewClient(server.URL, "test-node"), journal)
			payload, _ := runtimehelper.Map(binding)
			task := api.TaskEnvelope{TaskID: binding.TaskID, TaskRecordID: binding.TaskRecordID, NodeID: binding.NodeID, TaskType: "recover_tool_account_runtime", LeaseAttempt: 1, Payload: payload}
			if scenario == "wrong-poll" {
				task.TaskRecordID = binding.OriginalTaskRecordID
			}
			err = worker.executeTask(context.Background(), task)
			denied := scenario == "wrong-action-grant" || scenario == "wrong-action-lease" || scenario == "missing-grant" || scenario == "wrong-original" || scenario == "wrong-attempt" || scenario == "wrong-poll" || scenario == "wrong-lease"
			if denied {
				if err == nil || helperCalls.Load() != 0 || successes.Load() != 0 || failures.Load() != 0 {
					t.Fatal("invalid authority reached helper or settlement")
				}
				return
			}
			if scenario == "lost-lease" {
				if err == nil || helperCalls.Load() != 1 || renewals.Load() < 2 || successes.Load() != 0 || failures.Load() != 0 {
					t.Fatal("lost lease was settled or retained Helper work")
				}
				return
			}
			if err != nil || helperCalls.Load() != 1 {
				t.Fatalf("recovery did not inspect existing helper evidence: %v", err)
			}
			failed := scenario == "wrong-action-helper" || scenario == "wrong-helper" || scenario == "helper-failure"
			if failed && (failures.Load() != 1 || successes.Load() != 0) || !failed && (successes.Load() != 1 || failures.Load() != 0) {
				t.Fatal("wrong recovery settlement")
			}
		})
	}
}

func alternateRecoveryAction(binding accountmigration.Binding) accountmigration.Binding {
	if binding.Version == 1 {
		binding.Version, binding.Action = 2, "verify_source"
	} else if binding.Version == 2 {
		binding.Version, binding.Action = 3, "repair_source"
	} else {
		binding.Version, binding.Action = 2, "verify_source"
	}
	return binding
}
