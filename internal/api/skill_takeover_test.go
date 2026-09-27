package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const takeoverTestUpload = "11111111-1111-4111-8111-111111111111"
const takeoverTestHelper = "22222222-2222-4222-8222-222222222222"
const takeoverTestCheckpoint = "33333333-3333-4333-8333-333333333333"

func takeoverTestBinding() skillmanager.AccountTakeoverBinding {
	digest, _ := skillmanager.AccountInventoryDigest([]skillmanager.AccountWriter{})
	return skillmanager.AccountTakeoverBinding{
		Version: 1, NodeID: "11111111-1111-4111-8111-111111111110", UserID: "11111111-1111-4111-8111-111111111112",
		AccountID: "11111111-1111-4111-8111-111111111113", TakeoverID: "11111111-1111-4111-8111-111111111114",
		TaskID: "11111111-1111-4111-8111-111111111115", RuntimeBackend: "native", DirectoryEpoch: 3, InventoryDigest: digest,
	}
}

func takeoverTestView(binding skillmanager.AccountTakeoverBinding) SkillTakeover {
	return SkillTakeover{
		ProtocolVersion: 1, ManifestVersion: 1, TakeoverID: binding.TakeoverID, TaskID: binding.TaskID,
		NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, RuntimeBackend: binding.RuntimeBackend,
		DirectoryEpoch: binding.DirectoryEpoch, InventoryDigest: binding.InventoryDigest,
		Inventory: []skillmanager.AccountWriter{}, Status: "reserved",
	}
}

func takeoverTestEnvelope(view SkillTakeover) skillEnvelope[SkillTakeover] {
	committed := view.Status == "committed"
	return skillEnvelope[SkillTakeover]{SchemaVersion: 1, Status: view.Status, Committed: &committed, Data: view}
}

func takeoverTestFile(data []byte) skillmanager.Entry {
	digest := sha256.Sum256(data)
	return skillmanager.Entry{Path: "state.bin", Kind: "file", Mode: 0o600, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), ContentKind: "binary"}
}

func TestSkillTakeoverTransferBindsGrantCaptureFilesAndCommit(t *testing.T) {
	binding := takeoverTestBinding()
	content := append([]byte{0xff, 0}, bytes.Repeat([]byte("private state"), 100_000)...)
	entry := takeoverTestFile(content)
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{entry}}
	digest, _ := skillmanager.Digest(manifest)
	capture := skillmanager.AccountCapture{Version: 1, Binding: binding, HelperReceiptID: takeoverTestHelper, TreeDigest: digest}
	view := takeoverTestView(binding)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer node-token" || r.URL.Query().Get("task_id") != binding.TaskID {
			t.Error("request lost task authentication")
		}
		switch {
		case r.Method == http.MethodGet:
			if r.URL.Path != "/api/v1/node/skill-takeovers/"+binding.TakeoverID {
				t.Error("wrong reservation path")
			}
		case strings.HasSuffix(r.URL.Path, "/capture"):
			var payload struct {
				HelperReceiptID  string                `json:"helper_receipt_id"`
				DirectoryEpoch   int64                 `json:"directory_epoch"`
				InventoryDigest  string                `json:"inventory_digest"`
				WritersQuiescent bool                  `json:"writers_quiescent"`
				Manifest         skillmanager.Manifest `json:"manifest"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			actual, err := skillmanager.Digest(payload.Manifest)
			if err != nil || actual != digest || payload.HelperReceiptID != capture.HelperReceiptID || payload.DirectoryEpoch != binding.DirectoryEpoch || payload.InventoryDigest != binding.InventoryDigest || !payload.WritersQuiescent {
				t.Error("capture identity changed")
			}
			view.Status, view.HelperReceiptID, view.CaptureDigest = "uploading", &capture.HelperReceiptID, &digest
			upload := takeoverTestUpload
			view.UploadID, view.UploadAttempt = &upload, 1
		case r.Method == http.MethodPut:
			if r.URL.Query().Get("upload_id") != takeoverTestUpload || !strings.HasSuffix(r.URL.Path, "/files/"+entry.SHA256) || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Error("file scope changed")
			}
			received, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(received, content) {
				t.Error("stream bytes changed")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false,
				"data": map[string]any{"upload_id": takeoverTestUpload, "digest": entry.SHA256, "created": true}})
			return
		case strings.HasSuffix(r.URL.Path, "/complete"):
			if r.URL.Query().Get("upload_id") != takeoverTestUpload {
				t.Error("completion upload changed")
			}
			checkpoint := takeoverTestCheckpoint
			view.Status, view.CheckpointID = "committed", &checkpoint
		default:
			t.Error("unexpected route")
		}
		_ = json.NewEncoder(w).Encode(takeoverTestEnvelope(view))
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	grant, err := client.GetSkillTakeover(context.Background(), binding)
	if err != nil || grant.Status != "reserved" {
		t.Fatalf("grant: %v", err)
	}
	upload, err := client.BeginSkillTakeover(context.Background(), capture, manifest)
	if err != nil || upload.Status != "uploading" {
		t.Fatalf("capture: %v", err)
	}
	source := &takeoverTestSource{Reader: bytes.NewReader(content)}
	if err := client.PutSkillTakeoverFile(context.Background(), binding, *upload.UploadID, entry, source); err != nil {
		t.Fatal(err)
	}
	if source.closed.Load() != 1 {
		t.Fatal("stream was not closed exactly once")
	}
	complete, err := client.CompleteSkillTakeover(context.Background(), capture, *upload.UploadID)
	if err != nil || complete.CheckpointID == nil || *complete.CheckpointID != takeoverTestCheckpoint {
		t.Fatalf("complete: %v", err)
	}
}

type takeoverTestSource struct {
	io.Reader
	closed atomic.Int32
}

func (r *takeoverTestSource) Close() error { r.closed.Add(1); return nil }

func TestSkillTakeoverRejectsMalformedOrMismatchedGrants(t *testing.T) {
	cases := map[string]func(*skillEnvelope[SkillTakeover]){
		"node":              func(r *skillEnvelope[SkillTakeover]) { r.Data.NodeID = takeoverTestUpload },
		"user":              func(r *skillEnvelope[SkillTakeover]) { r.Data.UserID = takeoverTestUpload },
		"account":           func(r *skillEnvelope[SkillTakeover]) { r.Data.AccountID = takeoverTestUpload },
		"task":              func(r *skillEnvelope[SkillTakeover]) { r.Data.TaskID = takeoverTestUpload },
		"takeover":          func(r *skillEnvelope[SkillTakeover]) { r.Data.TakeoverID = takeoverTestUpload },
		"epoch":             func(r *skillEnvelope[SkillTakeover]) { r.Data.DirectoryEpoch++ },
		"backend":           func(r *skillEnvelope[SkillTakeover]) { r.Data.RuntimeBackend = "docker_sandbox" },
		"protocol":          func(r *skillEnvelope[SkillTakeover]) { r.Data.ProtocolVersion++ },
		"manifest":          func(r *skillEnvelope[SkillTakeover]) { r.Data.ManifestVersion++ },
		"schema":            func(r *skillEnvelope[SkillTakeover]) { r.SchemaVersion++ },
		"inventory":         func(r *skillEnvelope[SkillTakeover]) { r.Data.Inventory = nil },
		"digest":            func(r *skillEnvelope[SkillTakeover]) { r.Data.InventoryDigest = strings.Repeat("a", 64) },
		"phase":             func(r *skillEnvelope[SkillTakeover]) { r.Status, r.Data.Status = "future", "future" },
		"envelope":          func(r *skillEnvelope[SkillTakeover]) { r.Status = "uploading" },
		"premature_commit":  func(r *skillEnvelope[SkillTakeover]) { *r.Committed = true },
		"absent_commit":     func(r *skillEnvelope[SkillTakeover]) { r.Committed = nil },
		"partial_capture":   func(r *skillEnvelope[SkillTakeover]) { id := takeoverTestHelper; r.Data.HelperReceiptID = &id },
		"incomplete_upload": func(r *skillEnvelope[SkillTakeover]) { r.Status, r.Data.Status = "uploading", "uploading" },
		"incomplete_commit": func(r *skillEnvelope[SkillTakeover]) {
			r.Status, r.Data.Status = "committed", "committed"
			*r.Committed = true
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			binding := takeoverTestBinding()
			envelope := takeoverTestEnvelope(takeoverTestView(binding))
			change(&envelope)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(envelope) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").GetSkillTakeover(context.Background(), binding); err == nil {
				t.Fatal("unbound grant accepted")
			}
		})
	}
}

func TestSkillTakeoverRejectsDuplicateEmptyOversizedAndRedirectResponses(t *testing.T) {
	binding := takeoverTestBinding()
	valid, _ := json.Marshal(takeoverTestEnvelope(takeoverTestView(binding)))
	for _, body := range []string{"", "null", "{}", string(valid) + "{}", strings.Replace(string(valid), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), strings.Repeat(" ", maxSkillResponseBytes+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
		_, err := NewClient(server.URL, "node").GetSkillTakeover(context.Background(), binding)
		server.Close()
		if err == nil {
			t.Fatal("invalid response accepted")
		}
	}
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Store(true); _, _ = w.Write(valid) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	_, err := NewClient(origin.URL, "node-token").GetSkillTakeover(context.Background(), binding)
	if err == nil || redirected.Load() {
		t.Fatal("skill request followed a redirect")
	}
}

func TestSkillTakeoverReturnsOnlyBoundedErrorCodes(t *testing.T) {
	for _, code := range []string{"STATE_WRITERS_ACTIVE", "private state\nsecret"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"code": code, "message": "private contents"}}})
		}))
		_, err := NewClient(server.URL, "node").GetSkillTakeover(context.Background(), takeoverTestBinding())
		server.Close()
		var failure *HTTPError
		if !errors.As(err, &failure) || failure.StatusCode != 409 || failure.Message != "" || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe error: %v", err)
		}
		if code == "STATE_WRITERS_ACTIVE" && failure.Code != code {
			t.Fatal("stable error code lost")
		}
	}
}

func TestSkillTakeoverRejectsCorruptAndTruncatedStreams(t *testing.T) {
	original := []byte{0xff, 0, 'a', 'b'}
	entry := takeoverTestFile(original)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false,
			"data": map[string]any{"upload_id": takeoverTestUpload, "digest": entry.SHA256, "created": true}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "node")
	for _, data := range [][]byte{original[:3], append(bytes.Clone(original), 'x'), {0xff, 0, 'x', 'x'}} {
		if err := client.PutSkillTakeoverFile(context.Background(), takeoverTestBinding(), takeoverTestUpload, entry, io.NopCloser(bytes.NewReader(data))); err == nil {
			t.Fatal("corrupt stream accepted")
		}
	}
	entry.ContentKind = "text"
	if err := client.PutSkillTakeoverFile(context.Background(), takeoverTestBinding(), takeoverTestUpload, entry, io.NopCloser(bytes.NewReader(original))); err == nil {
		t.Fatal("incorrect classification accepted")
	}
}

type takeoverRoundTripper func(*http.Request) (*http.Response, error)

func (f takeoverRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSkillTakeoverDoesNotTrustEarlySuccessWithoutConsumingStream(t *testing.T) {
	entry := takeoverTestFile([]byte{0})
	client := NewClient("https://example.invalid", "node")
	client.httpClient.Transport = takeoverRoundTripper(func(r *http.Request) (*http.Response, error) {
		_ = r.Body.Close()
		body, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false,
			"data": map[string]any{"upload_id": takeoverTestUpload, "digest": entry.SHA256, "created": true}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})
	source := &takeoverTestSource{Reader: bytes.NewReader([]byte{0})}
	if err := client.PutSkillTakeoverFile(context.Background(), takeoverTestBinding(), takeoverTestUpload, entry, source); err == nil {
		t.Fatal("unconsumed stream acknowledged")
	}
	if source.closed.Load() != 1 {
		t.Fatal("source closure not owned")
	}
}

func TestSkillTakeoverValidatesBeforeNetworkAndHonorsCancellation(t *testing.T) {
	calls := 0
	client := NewClient("https://example.invalid", "node")
	client.httpClient.Transport = takeoverRoundTripper(func(r *http.Request) (*http.Response, error) { calls++; return nil, r.Context().Err() })
	binding := takeoverTestBinding()
	binding.TaskID = "../escape"
	if _, err := client.GetSkillTakeover(context.Background(), binding); err == nil || calls != 0 {
		t.Fatal("invalid task reached network")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.GetSkillTakeover(ctx, takeoverTestBinding())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestSkillTakeoverRejectsMismatchedFileAcknowledgements(t *testing.T) {
	entry := takeoverTestFile([]byte{0})
	for _, change := range []string{"upload", "digest", "committed", "created", "status", "schema"} {
		t.Run(change, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				data := map[string]any{"upload_id": takeoverTestUpload, "digest": entry.SHA256, "created": true}
				envelope := map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false, "data": data}
				switch change {
				case "upload":
					data["upload_id"] = takeoverTestHelper
				case "digest":
					data["digest"] = strings.Repeat("a", 64)
				case "committed":
					envelope["committed"] = true
				case "created":
					delete(data, "created")
				case "status":
					envelope["status"] = "committed"
				case "schema":
					envelope["schema_version"] = 2
				}
				_ = json.NewEncoder(w).Encode(envelope)
			}))
			defer server.Close()
			err := NewClient(server.URL, "node").PutSkillTakeoverFile(context.Background(), takeoverTestBinding(), takeoverTestUpload, entry, io.NopCloser(bytes.NewReader([]byte{0})))
			if err == nil {
				t.Fatal("incorrect file acknowledgement accepted")
			}
		})
	}
}

func TestSkillTakeoverCompletionRequiresOriginalCaptureAndUpload(t *testing.T) {
	binding := takeoverTestBinding()
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{}}
	digest, _ := skillmanager.Digest(manifest)
	capture := skillmanager.AccountCapture{Version: 1, Binding: binding, HelperReceiptID: takeoverTestHelper, TreeDigest: digest}
	for _, change := range []string{"upload", "helper", "digest", "uncommitted"} {
		t.Run(change, func(t *testing.T) {
			view := takeoverTestView(binding)
			upload, helper, tree, checkpoint := takeoverTestUpload, takeoverTestHelper, digest, takeoverTestCheckpoint
			view.Status, view.UploadAttempt = "committed", 1
			view.UploadID, view.HelperReceiptID, view.CaptureDigest, view.CheckpointID = &upload, &helper, &tree, &checkpoint
			switch change {
			case "upload":
				upload = takeoverTestCheckpoint
			case "helper":
				helper = takeoverTestCheckpoint
			case "digest":
				tree = strings.Repeat("a", 64)
			case "uncommitted":
				view.Status, view.CheckpointID = "uploading", nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(takeoverTestEnvelope(view))
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "node").CompleteSkillTakeover(context.Background(), capture, takeoverTestUpload); err == nil {
				t.Fatal("another capture was acknowledged")
			}
			if change == "helper" || change == "digest" {
				if _, err := NewClient(server.URL, "node").BeginSkillTakeover(context.Background(), capture, manifest); err == nil {
					t.Fatal("another capture was begun")
				}
			}
		})
	}
}

func TestSkillTakeoverSupportsBoundedLargeWriterInventory(t *testing.T) {
	binding := takeoverTestBinding()
	view := takeoverTestView(binding)
	writer := skillmanager.AccountWriter{Kind: "session", NodeID: binding.NodeID, ResourceID: binding.TaskID}
	view.Inventory = make([]skillmanager.AccountWriter, 10_000)
	for i := range view.Inventory {
		view.Inventory[i] = writer
	}
	digest, _ := skillmanager.AccountInventoryDigest(view.Inventory)
	binding.InventoryDigest, view.InventoryDigest = digest, digest
	encoded, _ := json.Marshal(takeoverTestEnvelope(view))
	if len(encoded) <= maxResponseBodyBytes || len(encoded) > maxSkillResponseBytes {
		t.Fatal("inventory fixture does not exercise the dedicated bound")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(encoded) }))
	defer server.Close()
	response, err := NewClient(server.URL, "node").GetSkillTakeover(context.Background(), binding)
	if err != nil || len(response.Inventory) != 10_000 {
		t.Fatalf("bounded inventory failed: %v", err)
	}
}

func TestSkillTakeoverPollPreservesSeparateLogicalAndRecordIdentities(t *testing.T) {
	binding := takeoverTestBinding()
	logical := "takeover_tool_account_skills:" + binding.TakeoverID
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tasks": []any{map[string]any{
			"task_id": logical, "task_record_id": binding.TaskID, "node_id": binding.NodeID,
			"task_type": "takeover_tool_account_skills",
		}}}})
	}))
	defer server.Close()
	polled, err := NewClient(server.URL, "node").PollTasks(context.Background())
	if err != nil || len(polled.Data.Tasks) != 1 {
		t.Fatalf("poll: %v", err)
	}
	task := polled.Data.Tasks[0]
	if task.TaskID != logical || task.TaskRecordID != binding.TaskID {
		t.Fatal("poll collapsed distinct task identities")
	}
}
