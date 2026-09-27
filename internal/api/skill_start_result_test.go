package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManagedStartConfirmationLiveServer(t *testing.T) {
	file := os.Getenv("AGENT_REMOTE_MANAGED_START_FIXTURE")
	if file == "" {
		t.Skip("requires disposable authenticated Server fixture")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		URL     string                `json:"url"`
		Token   string                `json:"token"`
		Task    string                `json:"task"`
		Binding SkillSnapshotIdentity `json:"binding"`
		Status  string                `json:"status"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal("invalid disposable fixture")
	}
	tmux := ""
	if fixture.Status == "running" {
		tmux = "managed-test"
	}
	result, err := NewManagedSessionStartResult(fixture.Binding, 3, fixture.Status, tmux)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(fixture.URL, fixture.Token)
	observed, err := client.InspectManagedSessionStart(context.Background(), fixture.Task, fixture.Binding, 3, result)
	if err != nil || observed.Accepted || observed.Result != result || observed.CurrentLeaseAttempt != 3 {
		t.Fatal("live Server did not inspect original absent receipt", err)
	}
	for range 2 {
		receipt, err := client.ConfirmManagedSessionStart(context.Background(), fixture.Task, fixture.Binding, 3, result)
		if err != nil || receipt != result {
			t.Fatal("live Server did not confirm exact original outcome", err)
		}
	}
	observed, err = client.InspectManagedSessionStart(context.Background(), fixture.Task, fixture.Binding, 3, result)
	if err != nil || !observed.Accepted || observed.Result != result {
		t.Fatal("live Server did not inspect original committed receipt", err)
	}
	result.LeaseAttempt = 4
	if _, err := client.ConfirmManagedSessionStart(context.Background(), fixture.Task, fixture.Binding, 4, result); err == nil {
		t.Fatal("live Server accepted changed committed attempt")
	}
}

func TestManagedStartConfirmationBindsOriginalOutcome(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	for _, status := range []string{"running", "stopped"} {
		t.Run(status, func(t *testing.T) {
			tmux := ""
			if status == "running" {
				tmux = "managed-test"
			}
			original, err := NewManagedSessionStartResult(binding, 3, status, tmux)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node-api/tasks/original-task/managed-start-result" || r.Header.Get("Authorization") != "Bearer node-start-test" {
					t.Error("confirmation lost original authenticated task")
				}
				var actual ManagedSessionStartResult
				if err := json.NewDecoder(r.Body).Decode(&actual); err != nil || actual != original {
					t.Error("confirmation changed original bounded outcome", err)
				}
				committed := true
				_ = json.NewEncoder(w).Encode(skillEnvelope[ManagedSessionStartResult]{SchemaVersion: 1, Status: "completed", Committed: &committed, Data: original})
			}))
			defer server.Close()
			client := NewClient(server.URL, "node-start-test")
			for range 2 {
				receipt, err := client.ConfirmManagedSessionStart(context.Background(), "original-task", binding, 3, original)
				if err != nil || receipt != original {
					t.Fatal("exact confirmation failed", err)
				}
			}
			if calls.Load() != 2 {
				t.Fatal("client implicitly retried a write")
			}
			changed := original
			changed.LeaseAttempt++
			if _, err := client.ConfirmManagedSessionStart(context.Background(), "original-task", binding, 3, changed); err == nil || calls.Load() != 2 {
				t.Fatal("changed attempt reached network")
			}
		})
	}
}

func TestManagedStartConfirmationRejectsUncertainOrForeignReceipt(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	original, err := NewManagedSessionStartResult(binding, 3, "running", "managed-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"status", "schema", "uncommitted", "errors", "attempt", "session", "task", "snapshot", "unit", "account", "missing", "alias", "null", "extra", "boolean", "duplicate", "mixed", "network"} {
		t.Run(change, func(t *testing.T) {
			data, _ := json.Marshal(original)
			var fields map[string]any
			_ = json.Unmarshal(data, &fields)
			envelope := map[string]any{"schema_version": 1, "status": "completed", "committed": true, "errors": []any{}, "data": fields}
			switch change {
			case "status":
				envelope["status"] = "pending"
			case "schema":
				envelope["schema_version"] = 2
			case "uncommitted":
				envelope["committed"] = false
			case "errors":
				envelope["errors"] = []any{map[string]any{"code": "FAILED"}}
			case "attempt":
				fields["lease_attempt"] = 4
			case "session", "task", "snapshot", "unit", "account":
				key := map[string]string{"session": "session_id", "task": "task_record_id", "snapshot": "skill_snapshot_id", "unit": "runtime_resource_id", "account": "tool_account_id"}[change]
				fields[key] = "changed"
			case "missing":
				delete(fields, "task_record_id")
			case "alias":
				fields["Task_Record_ID"] = fields["task_record_id"]
				delete(fields, "task_record_id")
			case "null":
				fields["task_record_id"] = nil
			case "extra":
				fields["runtime_uid"] = 12345
			case "boolean":
				fields["lease_attempt"] = true
			case "mixed":
				fields["code"] = "SKILL_START_STOPPED"
			}
			body, _ := json.Marshal(envelope)
			if change == "duplicate" {
				body = []byte(strings.Replace(string(body), `"lease_attempt":3`, `"lease_attempt":3,"lease_attempt":3`, 1))
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if change == "network" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client := NewClient(server.URL, "node-start-test")
			if _, err := client.ConfirmManagedSessionStart(context.Background(), "original-task", binding, 3, original); err == nil || calls.Load() != 1 {
				t.Fatal("uncertain confirmation accepted or retried", err)
			}
		})
	}
}
