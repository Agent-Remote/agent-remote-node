package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSkillSnapshotLeaseBindsTheExactPollAttempt(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	serverTime := time.Date(2040, 1, 1, 1, 0, 0, 0, time.UTC)
	lease := SkillSnapshotLease{SkillSnapshotIdentity: binding,
		LeaseAttempt: 9, ServerTime: serverTime, LeaseUntil: serverTime.Add(30 * time.Second), RenewAfterMilliseconds: 10000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Attempt int64 `json:"lease_attempt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/skill-snapshots/"+binding.SnapshotID+"/lease" || r.URL.Query().Get("task_id") != binding.TaskID || body.Attempt != 9 || r.Header.Get("Authorization") != "Bearer lease-test-node" {
			t.Error("renewal lost exact task/attempt authorization")
		}
		committed := false
		_ = json.NewEncoder(w).Encode(skillEnvelope[SkillSnapshotLease]{SchemaVersion: 1, Status: "leased", Committed: &committed, Data: lease})
	}))
	defer server.Close()
	actual, err := NewClient(server.URL, "lease-test-node").RenewSkillSnapshotLease(context.Background(), binding, 9)
	if err != nil || actual != lease {
		t.Fatalf("renewal: %#v %v", actual, err)
	}
}

func TestSkillSnapshotLeaseRejectsMalformedOrChangedAuthority(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	for _, change := range []string{"attempt", "task", "snapshot", "node", "missing_time", "expired", "duration", "interval", "committed", "status", "schema", "user", "account", "session", "backend"} {
		t.Run(change, func(t *testing.T) {
			now := time.Now().UTC()
			lease := SkillSnapshotLease{SkillSnapshotIdentity: binding,
				LeaseAttempt: 3, ServerTime: now, LeaseUntil: now.Add(30 * time.Second), RenewAfterMilliseconds: 10000}
			committed := false
			envelope := skillEnvelope[SkillSnapshotLease]{SchemaVersion: 1, Status: "leased", Committed: &committed, Data: lease}
			switch change {
			case "attempt":
				envelope.Data.LeaseAttempt++
			case "task":
				envelope.Data.TaskID = takeoverTestHelper
			case "snapshot":
				envelope.Data.SnapshotID = takeoverTestHelper
			case "node":
				envelope.Data.NodeID = takeoverTestHelper
			case "user":
				envelope.Data.UserID = takeoverTestHelper
			case "account":
				envelope.Data.AccountID = takeoverTestHelper
			case "session":
				envelope.Data.SessionID = takeoverTestCheckpoint
			case "backend":
				envelope.Data.RuntimeBackend = "docker_sandbox"
			case "missing_time":
				envelope.Data.ServerTime = time.Time{}
			case "expired":
				envelope.Data.LeaseUntil = now
			case "duration":
				envelope.Data.LeaseUntil = now.Add(time.Hour)
			case "interval":
				envelope.Data.RenewAfterMilliseconds = 30000
			case "committed":
				committed = true
			case "status":
				envelope.Status = "committed"
			case "schema":
				envelope.SchemaVersion++
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(envelope) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").RenewSkillSnapshotLease(context.Background(), binding, 3); err == nil {
				t.Fatal("invalid lease acknowledged")
			}
		})
	}
	for _, attempt := range []int64{0, -1, 2147483648} {
		if _, err := NewClient("https://example.invalid", "node").RenewSkillSnapshotLease(context.Background(), binding, attempt); err == nil {
			t.Fatal("invalid attempt reached network")
		}
	}
}

func snapshotLeaseTestBinding(t *testing.T) SkillSnapshotIdentity {
	t.Helper()
	snapshot, _ := snapshotTestView(t, nil)
	return snapshot.SkillSnapshotIdentity
}

func TestSkillSnapshotLeaseRejectsAliasedOrNullFields(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	now := time.Now().UTC()
	lease := SkillSnapshotLease{SkillSnapshotIdentity: binding, LeaseAttempt: 3, ServerTime: now, LeaseUntil: now.Add(30 * time.Second), RenewAfterMilliseconds: 10000}
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"alias", "null", "missing", "unknown", "fractional", "boolean"} {
		t.Run(change, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "alias":
				fields["TASK_ID"] = fields["task_id"]
				delete(fields, "task_id")
			case "null":
				fields["server_time"] = nil
			case "missing":
				delete(fields, "session_id")
			case "unknown":
				fields["extra"] = true
			case "fractional":
				fields["lease_attempt"] = 3.5
			case "boolean":
				fields["lease_attempt"] = true
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "leased", "committed": false, "data": fields, "errors": []any{}})
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").RenewSkillSnapshotLease(context.Background(), binding, 3); err == nil {
				t.Fatal("malformed authority accepted")
			}
		})
	}
}
