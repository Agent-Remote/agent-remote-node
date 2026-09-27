package api

import (
	"bytes"
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

func finalizationFixture() (SkillFinalizationInput, skillmanager.Manifest, SkillFinalization) {
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{takeoverTestFile([]byte{0xff, 0, 'a', 'b'})}}
	digest, _ := skillmanager.Digest(manifest)
	input := SkillFinalizationInput{
		SnapshotID: "55555555-5555-4555-8555-555555555555", SessionID: "66666666-6666-4666-8666-666666666666",
		IdempotencyKey: "finalization:55555555-5555-4555-8555-555555555555", TreeDigest: digest,
	}
	view := SkillFinalization{
		ID: takeoverTestHelper, SnapshotID: input.SnapshotID, IncomingDigest: digest, Status: "upload_pending",
		UploadID: takeoverTestUpload, UploadAttempt: 1, UploadStatus: "staged", ExpiresAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
	}
	return input, manifest, view
}

func finalizationEnvelope(view SkillFinalization) skillEnvelope[SkillFinalization] {
	committed := view.Status != "upload_pending"
	return skillEnvelope[SkillFinalization]{SchemaVersion: 1, Status: view.Status, Committed: &committed, Data: view}
}

func finalizationRetained(view SkillFinalization, unclean bool) SkillFinalization {
	checkpoint := takeoverTestCheckpoint
	view.Status, view.UploadStatus, view.CheckpointID, view.Unclean = "persisted", "committed", &checkpoint, unclean
	if unclean {
		view.Status = "persisted_unclean"
	}
	return view
}

func TestSkillFinalizationTransfersRetainedInputBeforeSeparatePublication(t *testing.T) {
	for _, unclean := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "unclean"}[unclean], func(t *testing.T) {
			input, manifest, view := finalizationFixture()
			input.Unclean, view.Unclean = unclean, unclean
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method+" "+r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer finalization-test" {
					t.Error("missing node authentication")
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/finalization"):
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 4 {
						t.Error("invalid finalization request", err)
					}
					var session, key string
					var captured skillmanager.Manifest
					var originalUnclean bool
					_ = json.Unmarshal(payload["session_id"], &session)
					_ = json.Unmarshal(payload["idempotency_key"], &key)
					_ = json.Unmarshal(payload["unclean"], &originalUnclean)
					_ = json.Unmarshal(payload["manifest"], &captured)
					digest, err := skillmanager.Digest(captured)
					if err != nil || session != input.SessionID || key != input.IdempotencyKey || originalUnclean != unclean || digest != input.TreeDigest || r.URL.Path != "/api/v1/node/skill-snapshots/"+input.SnapshotID+"/finalization" {
						t.Error("finalization input changed", err)
					}
				case r.Method == http.MethodPut:
					content, err := io.ReadAll(r.Body)
					if err != nil || !bytes.Equal(content, []byte{0xff, 0, 'a', 'b'}) || r.Header.Get("Content-Type") != "application/octet-stream" || r.URL.Query().Get("upload_id") != view.UploadID || r.URL.Path != "/api/v1/node/skill-finalizations/"+view.ID+"/files/"+manifest.Entries[0].SHA256 {
						t.Error("file upload changed identity or bytes", err)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false,
						"data": map[string]any{"upload_id": view.UploadID, "digest": manifest.Entries[0].SHA256, "created": true}})
					return
				case strings.HasSuffix(r.URL.Path, "/complete"):
					if r.URL.Query().Get("upload_id") != view.UploadID || r.Method != http.MethodPost {
						t.Error("completion lost upload scope")
					}
					view = finalizationRetained(view, unclean)
				case strings.HasSuffix(r.URL.Path, "/publish"):
					publication := SkillPublication{ID: input.SessionID, FinalizationID: view.ID, Attempt: 1, Status: "published", ResultCheckpointID: view.CheckpointID}
					if unclean {
						reason := "unclean"
						publication.Status, publication.Reason, publication.ResultCheckpointID = "detached", &reason, nil
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": publication.Status, "committed": true, "data": publication})
					return
				default:
					if r.Method != http.MethodGet || r.URL.Path != "/api/v1/node/skill-finalizations/"+view.ID || r.URL.RawQuery != "" {
						t.Error("unexpected finalization route")
					}
				}
				_ = json.NewEncoder(w).Encode(finalizationEnvelope(view))
			}))
			defer server.Close()
			client, ctx := NewClient(server.URL, "finalization-test"), context.Background()
			begun, err := client.BeginSkillFinalization(ctx, input, manifest, nil)
			if err != nil || begun.Status != "upload_pending" || begun.CheckpointID != nil {
				t.Fatal("begin falsely acknowledged persistence", err)
			}
			source := &takeoverTestSource{Reader: bytes.NewReader([]byte{0xff, 0, 'a', 'b'})}
			if err := client.PutSkillFinalizationFile(ctx, input, begun, manifest.Entries[0], source); err != nil || source.closed.Load() != 1 {
				t.Fatal("file transfer failed or leaked source", err)
			}
			retained, err := client.CompleteSkillFinalization(ctx, input, begun)
			if err != nil || retained.CheckpointID == nil || retained.Status == "published" {
				t.Fatal("complete conflated persistence and publication", err)
			}
			observed, err := client.GetSkillFinalization(ctx, input, retained)
			if err != nil || *observed.CheckpointID != *retained.CheckpointID {
				t.Fatal("status lost retained input", err)
			}
			published, err := client.PublishSkillFinalization(ctx, input, observed)
			if err != nil || unclean && published.Status != "detached" || !unclean && published.Status != "published" {
				t.Fatal("wrong publication decision", err)
			}
			if len(methods) != 5 {
				t.Fatal("transport implicitly retried")
			}
		})
	}
}

func TestSkillFinalizationAcceptsRenewalOnlyWithStableOriginalIdentity(t *testing.T) {
	input, manifest, previous := finalizationFixture()
	previous.UploadStatus = "expired"
	for _, change := range []string{"renew", "id", "same_id", "same_attempt", "older_attempt", "checkpoint_loss"} {
		t.Run(change, func(t *testing.T) {
			prior, next := previous, previous
			next.UploadID, next.UploadAttempt, next.UploadStatus = takeoverTestCheckpoint, 2, "staged"
			switch change {
			case "id":
				next.ID = input.SessionID
			case "same_id":
				next.UploadID = prior.UploadID
			case "same_attempt":
				next.UploadAttempt = prior.UploadAttempt
			case "older_attempt":
				prior.UploadAttempt, next.UploadAttempt = 3, 2
			case "checkpoint_loss":
				prior = finalizationRetained(prior, false)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(finalizationEnvelope(next))
			}))
			defer server.Close()
			client := NewClient(server.URL, "node")
			_, err := client.BeginSkillFinalization(context.Background(), input, manifest, &prior)
			if (err == nil) != (change == "renew") {
				t.Fatal("unexpected renewal result", err)
			}
			_, err = client.GetSkillFinalization(context.Background(), input, prior)
			if (err == nil) != (change == "renew") {
				t.Fatal("unexpected observed attempt", err)
			}
			if _, err := client.CompleteSkillFinalization(context.Background(), input, prior); err == nil {
				t.Fatal("completion accepted uncommitted replacement upload")
			}
		})
	}
}
