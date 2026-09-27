package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManagedStartInspectionPreservesAcceptanceAndCurrentAttempt(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	for _, status := range []string{"running", "stopped"} {
		for _, accepted := range []bool{false, true} {
			tmux := ""
			if status == "running" {
				tmux = "original"
			}
			outcome, err := NewManagedSessionStartResult(binding, 3, status, tmux)
			if err != nil {
				t.Fatal(err)
			}
			observation := ManagedStartObservation{Result: outcome, Accepted: accepted, CurrentLeaseAttempt: 4, TaskStatus: "leased"}
			envelopeStatus := "unconfirmed"
			if accepted {
				envelopeStatus = "completed"
				observation.TaskStatus = "failed"
				if status == "running" {
					observation.TaskStatus = "succeeded"
				}
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/node-api/tasks/original/managed-start-result/inspect" || r.Header.Get("Authorization") != "Bearer test-inspection" {
					t.Error("inspection lost original authenticated task")
				}
				var proposed ManagedSessionStartResult
				if err := json.NewDecoder(r.Body).Decode(&proposed); err != nil || proposed != outcome {
					t.Error("inspection changed its proposed outcome", err)
				}
				_ = json.NewEncoder(w).Encode(skillEnvelope[ManagedStartObservation]{SchemaVersion: 1, Status: envelopeStatus, Committed: &accepted, Data: observation})
			}))
			observed, err := NewClient(server.URL, "test-inspection").InspectManagedSessionStart(context.Background(), "original", binding, 3, outcome)
			server.Close()
			if err != nil || observed != observation || calls.Load() != 1 {
				t.Fatal("exact observation rejected or retried", err)
			}
		}
	}
}

func TestManagedStartInspectionRejectsAmbiguousEvidence(t *testing.T) {
	binding := snapshotLeaseTestBinding(t)
	outcome, err := NewManagedSessionStartResult(binding, 3, "running", "original")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"commit", "status", "accepted_terminal", "absent_terminal", "negative", "overflow", "result", "extra", "missing", "alias", "null", "duplicate", "network"} {
		t.Run(change, func(t *testing.T) {
			fields := map[string]any{"result": outcome, "accepted": false, "current_lease_attempt": 4, "task_status": "leased"}
			envelope := map[string]any{"schema_version": 1, "status": "unconfirmed", "committed": false, "data": fields}
			switch change {
			case "commit":
				envelope["committed"] = true
			case "status":
				envelope["status"] = "completed"
			case "accepted_terminal":
				envelope["committed"], envelope["status"], fields["accepted"] = true, "completed", true
			case "absent_terminal":
				fields["task_status"] = "succeeded"
			case "negative":
				fields["current_lease_attempt"] = -1
			case "overflow":
				fields["current_lease_attempt"] = int64(2147483648)
			case "result":
				changed := outcome
				changed.LeaseAttempt++
				fields["result"] = changed
			case "extra":
				fields["lease_until"] = "invented-grant"
			case "missing":
				delete(fields, "accepted")
			case "alias":
				delete(fields, "accepted")
				fields["Accepted"] = false
			case "null":
				fields["accepted"] = nil
			}
			body, _ := json.Marshal(envelope)
			if change == "duplicate" {
				body = []byte(strings.Replace(string(body), `"accepted":false`, `"accepted":false,"accepted":false`, 1))
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if change == "network" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "test-inspection").InspectManagedSessionStart(context.Background(), "original", binding, 3, outcome); err == nil || calls.Load() != 1 {
				t.Fatal("ambiguous observation accepted or retried", err)
			}
		})
	}
}
