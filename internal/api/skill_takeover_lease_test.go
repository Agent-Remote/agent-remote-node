package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSkillTakeoverLeaseBindsTheExactPollAttempt(t *testing.T) {
	binding := takeoverTestBinding()
	serverTime := time.Date(2040, 1, 1, 1, 0, 0, 0, time.UTC)
	lease := SkillTakeoverLease{TakeoverID: binding.TakeoverID, TaskID: binding.TaskID, NodeID: binding.NodeID,
		LeaseAttempt: 9, ServerTime: serverTime, LeaseUntil: serverTime.Add(30 * time.Second), RenewAfterMilliseconds: 10000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Attempt int64 `json:"lease_attempt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node/skill-takeovers/"+binding.TakeoverID+"/lease" || r.URL.Query().Get("task_id") != binding.TaskID || body.Attempt != 9 || r.Header.Get("Authorization") != "Bearer lease-test-node" {
			t.Error("renewal lost exact task/attempt authorization")
		}
		committed := false
		_ = json.NewEncoder(w).Encode(skillEnvelope[SkillTakeoverLease]{SchemaVersion: 1, Status: "leased", Committed: &committed, Data: lease})
	}))
	defer server.Close()
	actual, err := NewClient(server.URL, "lease-test-node").RenewSkillTakeoverLease(context.Background(), binding, 9)
	if err != nil || actual != lease {
		t.Fatalf("renewal: %#v %v", actual, err)
	}
}

func TestSkillTakeoverLeaseRejectsMalformedOrChangedAuthority(t *testing.T) {
	binding := takeoverTestBinding()
	for _, change := range []string{"attempt", "task", "takeover", "node", "missing_time", "expired", "duration", "interval", "committed", "status", "schema"} {
		t.Run(change, func(t *testing.T) {
			now := time.Now().UTC()
			lease := SkillTakeoverLease{TakeoverID: binding.TakeoverID, TaskID: binding.TaskID, NodeID: binding.NodeID,
				LeaseAttempt: 3, ServerTime: now, LeaseUntil: now.Add(30 * time.Second), RenewAfterMilliseconds: 10000}
			committed := false
			envelope := skillEnvelope[SkillTakeoverLease]{SchemaVersion: 1, Status: "leased", Committed: &committed, Data: lease}
			switch change {
			case "attempt":
				envelope.Data.LeaseAttempt++
			case "task":
				envelope.Data.TaskID = takeoverTestHelper
			case "takeover":
				envelope.Data.TakeoverID = takeoverTestHelper
			case "node":
				envelope.Data.NodeID = takeoverTestHelper
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
			if _, err := NewClient(server.URL, "node").RenewSkillTakeoverLease(context.Background(), binding, 3); err == nil {
				t.Fatal("invalid lease acknowledged")
			}
		})
	}
	for _, attempt := range []int64{0, -1, 2147483648} {
		if _, err := NewClient("https://example.invalid", "node").RenewSkillTakeoverLease(context.Background(), binding, attempt); err == nil {
			t.Fatal("invalid attempt reached network")
		}
	}
}
