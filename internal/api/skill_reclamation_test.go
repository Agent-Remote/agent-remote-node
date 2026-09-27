package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func reclamationFixture() (skillmanager.FinalizationAcknowledgement, skillmanager.ReclamationAuthorization) {
	input, _, receipt := finalizationFixture()
	receipt = finalizationRetained(receipt, false)
	binding := takeoverTestBinding()
	capture := skillmanager.FinalizationRecord{Version: 1, ObjectsVersion: 1, State: "published", TreeDigest: input.TreeDigest,
		Binding: skillmanager.SnapshotBinding{NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID,
			SessionID: input.SessionID, SnapshotID: input.SnapshotID, DirectoryEpoch: 1, InitialTreeDigest: input.TreeDigest}}
	publication := skillmanager.PublicationReceipt{ID: takeoverTestUpload, FinalizationID: receipt.ID,
		Attempt: 9007199254740993, Status: "published", ResultCheckpointID: receipt.CheckpointID}
	ack := skillmanager.FinalizationAcknowledgement{Version: 1, Capture: capture, Receipt: receipt, Publication: &publication}
	// A different Server wall clock must not extend or destroy the conservative local duration.
	verified := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	authority := skillmanager.ReclamationAuthorization{Version: 1, RequestID: takeoverTestHelper,
		NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, SessionID: input.SessionID,
		SnapshotID: input.SnapshotID, FinalizationID: receipt.ID, CheckpointID: *receipt.CheckpointID,
		TreeDigest: input.TreeDigest, PublicationID: publication.ID, PublicationAttempt: publication.Attempt,
		PublicationStatus: "published", VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
	return ack, authority
}

func reclamationEnvelope(authority skillmanager.ReclamationAuthorization) map[string]any {
	return map[string]any{"schema_version": 1, "status": "reclaimable", "committed": true, "retryable": false, "errors": []any{}, "data": authority}
}

func TestSkillReclamationBindsFreshChallengeAndOriginalInput(t *testing.T) {
	ack, authority := reclamationFixture()
	requests := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		challenge := r.URL.Query().Get("request_id")
		if r.Method != http.MethodGet || r.URL.Path != finalizationPath(ack.Receipt, "/reclamation-authorization", false) ||
			r.Header.Get("Authorization") != "Bearer reclamation-test" || !validSkillUUID(challenge) || requests[challenge] || len(r.URL.Query()) != 1 {
			t.Error("reclamation changed request identity or reused a challenge")
		}
		requests[challenge] = true
		authority.RequestID = challenge
		_ = json.NewEncoder(w).Encode(reclamationEnvelope(authority))
	}))
	defer server.Close()
	for range 2 {
		started := time.Now()
		grant, err := NewClient(server.URL, "reclamation-test").AuthorizeSkillReclamation(context.Background(), ack)
		if err != nil {
			t.Fatal(err)
		}
		if grant.Authorization.PublicationAttempt != 9007199254740993 ||
			grant.Deadline.Before(started.Add(time.Minute)) || time.Until(grant.Deadline) > time.Minute {
			t.Fatal("authorization rounded a generation or trusted Server wall time")
		}
	}
}

func TestSkillReclamationRejectsChangedOrIncompleteAuthority(t *testing.T) {
	ack, authority := reclamationFixture()
	encoded, _ := json.Marshal(authority)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	for field := range fields {
		for _, change := range []string{"missing", "null", "alias", "duplicate"} {
			t.Run(field+"/"+change, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					authority.RequestID = r.URL.Query().Get("request_id")
					encoded, _ := json.Marshal(authority)
					var data map[string]json.RawMessage
					_ = json.Unmarshal(encoded, &data)
					switch change {
					case "missing":
						delete(data, field)
					case "null":
						data[field] = json.RawMessage("null")
					case "alias":
						data[strings.ToUpper(field)] = data[field]
						delete(data, field)
					}
					body, _ := json.Marshal(data)
					if change == "duplicate" {
						body = append([]byte(`{"`+field+`":`+string(data[field])+`,`), body[1:]...)
					}
					_, _ = io.WriteString(w, `{"schema_version":1,"status":"reclaimable","committed":true,"retryable":false,"data":`+string(body)+`}`)
				}))
				defer server.Close()
				if _, err := NewClient(server.URL, "node").AuthorizeSkillReclamation(context.Background(), ack); err == nil {
					t.Fatal("ambiguous reclamation authority accepted")
				}
			})
		}
	}
	for _, field := range []string{"request_id", "node_id", "user_id", "account_id", "session_id", "snapshot_id", "finalization_id", "checkpoint_id", "publication_id", "tree_digest", "unclean", "publication_attempt", "publication_status", "expires_at", "extra", "retryable", "committed", "schema_version", "status"} {
		t.Run("changed/"+field, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authority.RequestID = r.URL.Query().Get("request_id")
				body, _ := json.Marshal(authority)
				var data map[string]json.RawMessage
				_ = json.Unmarshal(body, &data)
				envelope := reclamationEnvelope(authority)
				switch field {
				case "tree_digest":
					data[field], _ = json.Marshal(strings.Repeat("f", 64))
				case "unclean":
					data[field] = json.RawMessage("true")
				case "publication_attempt":
					data[field] = json.RawMessage("9007199254740992")
				case "publication_status":
					data[field] = json.RawMessage(`"superseded"`)
				case "expires_at":
					data[field], _ = json.Marshal(authority.ExpiresAt.Add(time.Second))
				case "extra":
					data["path"] = json.RawMessage(`"/untrusted"`)
				case "retryable":
					envelope[field] = true
				case "committed":
					envelope[field] = false
				case "schema_version":
					envelope[field] = 2
				case "status":
					envelope[field] = "persisted"
				default:
					data[field] = json.RawMessage(`"99999999-9999-4999-8999-999999999999"`)
				}
				envelope["data"] = data
				_ = json.NewEncoder(w).Encode(envelope)
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").AuthorizeSkillReclamation(context.Background(), ack); err == nil {
				t.Fatal("changed reclamation input accepted")
			}
		})
	}
}

func TestSkillReclamationChargesElapsedRequestTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ack, authority := reclamationFixture()
		client := NewClient("https://server.invalid", "node")
		client.httpClient.Transport = snapshotRoundTripper(func(r *http.Request) (*http.Response, error) {
			time.Sleep(61 * time.Second)
			authority.RequestID = r.URL.Query().Get("request_id")
			body, _ := json.Marshal(reclamationEnvelope(authority))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
		})
		if _, err := client.AuthorizeSkillReclamation(context.Background(), ack); err == nil || !strings.Contains(err.Error(), "budget expired") {
			t.Fatal("late response regained a fresh authorization window", err)
		}
	})
}

func TestSkillReclamationRejectsPendingAcknowledgementBeforeHTTP(t *testing.T) {
	ack, _ := reclamationFixture()
	ack.Publication = nil
	if _, err := NewClient("http://127.0.0.1:1", "node").AuthorizeSkillReclamation(context.Background(), ack); err == nil || !strings.Contains(err.Error(), "saved terminal") {
		t.Fatal("unpublished input requested reclamation", err)
	}
}

func TestSkillReclamationNeverFollowsRedirectOrRetriesServerFailure(t *testing.T) {
	ack, _ := reclamationFixture()
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").AuthorizeSkillReclamation(context.Background(), ack); err == nil || calls.Load() != 1 || followed.Load() != 0 {
				t.Fatal("reclamation followed a redirect or retried an unavailable authority", err)
			}
		})
	}
}

func TestSkillReclamationCancellationJoinsRequest(t *testing.T) {
	ack, _ := reclamationFixture()
	started, finished := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(finished)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(server.URL, "node").AuthorizeSkillReclamation(ctx, ack)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("reclamation request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled reclamation returned authority")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled reclamation request remained live")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("reclamation left its HTTP request open")
	}
}

func TestSkillReclamationEchoesValidatedHelperChallenge(t *testing.T) {
	ack, authority := reclamationFixture()
	challenge := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("request_id") != challenge {
			t.Error("Helper challenge was replaced before HTTP")
		}
		authority.RequestID = challenge
		_ = json.NewEncoder(w).Encode(reclamationEnvelope(authority))
	}))
	defer server.Close()
	client := NewClient(server.URL, "node")
	for _, invalid := range []string{"", "not-a-uuid", strings.ToUpper(challenge), "00000000-0000-0000-0000-000000000000"} {
		if _, err := client.AuthorizeSkillReclamationChallenge(context.Background(), invalid, ack); err == nil {
			t.Fatal("invalid Helper challenge reached Server")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("challenge validation followed HTTP")
	}
	if grant, err := client.AuthorizeSkillReclamationChallenge(context.Background(), challenge, ack); err != nil || grant.RequestID != challenge || calls.Load() != 1 {
		t.Fatal("live Helper challenge could not obtain original authorization", err)
	}
}
