package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkillFinalizationRejectsIncompleteAmbiguousOrChangedReceipts(t *testing.T) {
	input, manifest, view := finalizationFixture()
	encoded, _ := json.Marshal(view)
	var original map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &original)
	for field := range original {
		for _, change := range []string{"missing", "alias", "null", "duplicate"} {
			if field == "checkpoint_id" && change == "null" {
				continue
			}
			t.Run(field+"/"+change, func(t *testing.T) {
				fields := make(map[string]json.RawMessage, len(original))
				for key, value := range original {
					fields[key] = value
				}
				switch change {
				case "missing":
					delete(fields, field)
				case "alias":
					fields[strings.ToUpper(field)] = fields[field]
					delete(fields, field)
				case "null":
					fields[field] = json.RawMessage("null")
				}
				body, _ := json.Marshal(fields)
				if change == "duplicate" {
					body = append([]byte(`{"`+field+`":`+string(original[field])+`,`), body[1:]...)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, `{"schema_version":1,"status":"upload_pending","committed":false,"data":`+string(body)+`}`)
				}))
				defer server.Close()
				if _, err := NewClient(server.URL, "node").BeginSkillFinalization(context.Background(), input, manifest, nil); err == nil {
					t.Fatal("ambiguous receipt accepted")
				}
			})
		}
	}
	for _, change := range []string{"snapshot", "digest", "unclean", "id", "upload", "attempt", "fraction", "expiry", "status", "premature_checkpoint", "upload_status", "schema", "envelope", "committed", "extra"} {
		t.Run(change, func(t *testing.T) {
			body, _ := json.Marshal(finalizationEnvelope(view))
			var envelope map[string]any
			_ = json.Unmarshal(body, &envelope)
			data := envelope["data"].(map[string]any)
			switch change {
			case "snapshot":
				data["snapshot_id"] = input.SessionID
			case "digest":
				data["incoming_digest"] = strings.Repeat("a", 64)
			case "unclean":
				data["unclean"] = true
			case "id":
				data["id"] = "../foreign"
			case "upload":
				data["upload_id"] = ""
			case "attempt":
				data["upload_attempt"] = 0
			case "fraction":
				data["upload_attempt"] = 1.5
			case "expiry":
				data["expires_at"] = "invalid"
			case "status":
				data["status"], envelope["status"] = "future", "future"
			case "premature_checkpoint":
				data["checkpoint_id"] = takeoverTestCheckpoint
			case "upload_status":
				data["upload_status"] = "committed"
			case "schema":
				envelope["schema_version"] = 2
			case "envelope":
				envelope["status"] = "persisted"
			case "committed":
				envelope["committed"] = true
			case "extra":
				data["path"] = "/untrusted"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(envelope) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").BeginSkillFinalization(context.Background(), input, manifest, nil); err == nil {
				t.Fatal("changed receipt accepted")
			}
		})
	}
}

func TestSkillFinalizationPublicationDecisionsRemainDistinct(t *testing.T) {
	input, _, view := finalizationFixture()
	view = finalizationRetained(view, false)
	for _, status := range []string{"published", "conflicted", "detached", "superseded"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(status+map[bool]string{false: "/valid", true: "/invalid"}[corrupt], func(t *testing.T) {
				publication := SkillPublication{ID: input.SessionID, FinalizationID: view.ID, Attempt: 2, Status: status}
				switch status {
				case "published":
					publication.ResultCheckpointID = view.CheckpointID
				case "conflicted":
					publication.ConflictCount = 1
				case "detached":
					reason := "directory_epoch_changed"
					publication.Reason = &reason
				case "superseded":
					reason := "state_reset"
					publication.Reason, publication.ConflictCount = &reason, 2
				}
				if corrupt {
					if status == "published" {
						publication.ConflictCount = 1
					} else {
						publication.ResultCheckpointID = view.CheckpointID
					}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": status, "committed": true, "data": publication})
				}))
				defer server.Close()
				_, err := NewClient(server.URL, "node").PublishSkillFinalization(context.Background(), input, view)
				if (err != nil) != corrupt {
					t.Fatal("publication decision was conflated", err)
				}
			})
		}
	}
}

func TestSkillPublicationRejectsIncompleteOrForeignFields(t *testing.T) {
	input, _, retained := finalizationFixture()
	retained = finalizationRetained(retained, false)
	for _, change := range []string{"foreign", "id", "attempt", "count", "reason", "missing_nullable", "missing_count", "null_count", "alias", "extra", "duplicate", "uncommitted"} {
		t.Run(change, func(t *testing.T) {
			fields := map[string]any{"id": input.SessionID, "finalization_id": retained.ID, "attempt": 1, "status": "conflicted", "conflict_count": 1, "reason": nil, "result_checkpoint_id": nil}
			committed := true
			switch change {
			case "foreign":
				fields["finalization_id"] = input.SnapshotID
			case "id":
				fields["id"] = ""
			case "attempt":
				fields["attempt"] = 0
			case "count":
				fields["conflict_count"] = -1
			case "reason":
				fields["reason"] = "private\ncontents"
			case "missing_nullable":
				delete(fields, "reason")
			case "missing_count":
				delete(fields, "conflict_count")
			case "null_count":
				fields["conflict_count"] = nil
			case "alias":
				fields["Status"] = fields["status"]
				delete(fields, "status")
			case "extra":
				fields["path"] = "/private"
			case "uncommitted":
				committed = false
			}
			encoded, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "conflicted", "committed": committed, "data": fields})
			if change == "duplicate" {
				encoded = []byte(strings.Replace(string(encoded), `"attempt":1`, `"attempt":1,"attempt":1`, 1))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(encoded) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").PublishSkillFinalization(context.Background(), input, retained); err == nil {
				t.Fatal("invalid publication accepted")
			}
		})
	}
}

func TestSkillFinalizationValidatesBeforeNetworkAndRetainsCancellation(t *testing.T) {
	input, manifest, view := finalizationFixture()
	calls := 0
	client := NewClient("https://example.invalid", "node")
	client.httpClient.Transport = takeoverRoundTripper(func(r *http.Request) (*http.Response, error) { calls++; return nil, r.Context().Err() })
	bad := input
	bad.SessionID = "../escape"
	if _, err := client.BeginSkillFinalization(context.Background(), bad, manifest, nil); err == nil || calls != 0 {
		t.Fatal("invalid identity reached network")
	}
	bad = input
	bad.TreeDigest = strings.Repeat("a", 64)
	if _, err := client.BeginSkillFinalization(context.Background(), bad, manifest, nil); err == nil || calls != 0 {
		t.Fatal("changed capture reached network")
	}
	if _, err := client.PublishSkillFinalization(context.Background(), input, view); err == nil || calls != 0 {
		t.Fatal("unpersisted input reached publication")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetSkillFinalization(ctx, input, view); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
