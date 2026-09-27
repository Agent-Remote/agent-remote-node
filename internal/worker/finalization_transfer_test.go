package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type finalizationFixture struct {
	t                   *testing.T
	mu                  sync.Mutex
	capture             skillmanager.FinalizationRecord
	manifest            skillmanager.Manifest
	view                api.SkillFinalization
	publication         api.SkillPublication
	client              api.Client
	journal             finalizationTransferJournal
	path, object, fault string
	calls               []string
	handles             []*os.File
	holds               []*os.File
	page                *skillmanager.FinalizationPage
	corruptObject       bool
	localFailure        string
	helperFault         string
	helperAcks          []string
	cleanupCalls        int
	reclamationCalls    int
	resumeCalls         int
	observation         *runtimehelper.SkillSessionObservation
	reconciliationError error
}

func (f *finalizationFixture) ReconcileSkillSession(_ context.Context, _, nodeID, sessionID string) (runtimehelper.SkillSessionObservation, error) {
	if f.reconciliationError != nil {
		return runtimehelper.SkillSessionObservation{}, f.reconciliationError
	}
	if f.observation != nil {
		return *f.observation, nil
	}
	return runtimehelper.SkillSessionObservation{SessionID: sessionID, State: "not_started"}, nil
}

func newFinalizationFixture(t *testing.T, unclean bool, status string) *finalizationFixture {
	t.Helper()
	content := []byte("learned\x00\xff")
	sum := sha256.Sum256(content)
	entry := skillmanager.Entry{Path: "a", Kind: "file", Mode: 0600, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), ContentKind: "binary"}
	duplicate := entry
	duplicate.Path = "b"
	f := &finalizationFixture{t: t, manifest: skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{entry, duplicate}}, path: filepath.Join(t.TempDir(), "transfers.json"), object: filepath.Join(t.TempDir(), "object")}
	if err := os.WriteFile(f.object, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := skillmanager.Digest(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.capture = skillmanager.FinalizationRecord{Version: 1, ObjectsVersion: 1, State: "local_durable", TreeDigest: digest, Unclean: unclean, Binding: skillmanager.SnapshotBinding{
		NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", AccountID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444", SnapshotID: "55555555-5555-4555-8555-555555555555", DirectoryEpoch: 9007199254740993, LibraryGeneration: 9007199254740995, InitialTreeDigest: digest, TaskID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", PreparationDigest: strings.Repeat("d", 64)}}
	f.view = api.SkillFinalization{ID: "66666666-6666-4666-8666-666666666666", SnapshotID: f.capture.Binding.SnapshotID, IncomingDigest: digest, Unclean: unclean, Status: "upload_pending", UploadID: "77777777-7777-4777-8777-777777777777", UploadAttempt: 1, UploadStatus: "staged", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	checkpoint := "88888888-8888-4888-8888-888888888888"
	f.publication = api.SkillPublication{ID: "99999999-9999-4999-8999-999999999999", FinalizationID: f.view.ID, Attempt: 1, Status: status}
	switch status {
	case "published":
		f.publication.ResultCheckpointID = &checkpoint
	case "conflicted":
		f.publication.ConflictCount = 2
	case "detached":
		reason := "unclean"
		if !unclean {
			reason = "directory_epoch_changed"
		}
		f.publication.Reason = &reason
	case "superseded":
		reason := "branch_changed"
		f.publication.Reason = &reason
	}
	f.reopen()
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	f.client = api.NewClient(server.URL, "finalization-test")
	t.Cleanup(func() {
		for _, h := range append(f.handles, f.holds...) {
			if _, err := h.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Error("object descriptor leaked", err)
				_ = h.Close()
			}
		}
	})
	return f
}

func (f *finalizationFixture) reopen() {
	f.t.Helper()
	store, err := ledger.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.journal = finalizationTransferJournal{ledger: store}
}

func (f *finalizationFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	phase := "get"
	var termination json.RawMessage
	switch {
	case strings.HasSuffix(r.URL.Path, "/capture-pending"):
		f.calls = append(f.calls, "capture_pending")
		if f.fault == "capture_pending" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var body json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "stopped", "committed": true, "data": body})
		return
	case strings.HasSuffix(r.URL.Path, "/termination"):
		phase = "termination"
		termination, _ = io.ReadAll(r.Body)
	case strings.HasSuffix(r.URL.Path, "/finalization"):
		phase = "begin"
	case strings.Contains(r.URL.Path, "/files/"):
		phase = "put"
	case strings.HasSuffix(r.URL.Path, "/complete"):
		phase = "complete"
	case strings.HasSuffix(r.URL.Path, "/publish"):
		phase = "publish"
	case strings.HasSuffix(r.URL.Path, "/reclamation-authorization"):
		phase = "reclaim"
	}
	f.calls = append(f.calls, phase)
	if r.Header.Get("Authorization") != "Bearer finalization-test" {
		f.t.Error("missing Node authentication")
	}
	pending, err := f.journal.load(f.capture)
	if err != nil || pending == nil {
		f.t.Error("HTTP preceded durable original input", err)
	}
	switch phase {
	case "begin":
		var body struct {
			SessionID      string                `json:"session_id"`
			IdempotencyKey string                `json:"idempotency_key"`
			Manifest       skillmanager.Manifest `json:"manifest"`
			Unclean        bool                  `json:"unclean"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Error(err)
		}
		digest, _ := skillmanager.Digest(body.Manifest)
		if body.SessionID != f.capture.Binding.SessionID || body.IdempotencyKey != finalizationInput(f.capture).IdempotencyKey || digest != f.capture.TreeDigest || body.Unclean != f.capture.Unclean {
			f.t.Error("changed original begin")
		}
		if f.view.UploadStatus == "expired" {
			f.view.UploadID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			f.view.UploadAttempt++
			f.view.UploadStatus = "staged"
			f.view.ExpiresAt = time.Now().UTC().Add(time.Hour)
		}
	case "put":
		if r.URL.Query().Get("upload_id") != f.view.UploadID {
			f.t.Error("wrong upload attempt")
		}
		bytes, err := io.ReadAll(r.Body)
		if (err != nil || skillmanager.VerifyContent(f.manifest.Entries[0], bytes) != nil) && !f.corruptObject {
			f.t.Error("changed object bytes", err)
		}
		if pending == nil || pending.record.Receipt == nil || pending.record.Receipt.UploadID != f.view.UploadID {
			f.t.Error("upload preceded durable attempt")
		}
	case "complete":
		if r.URL.Query().Get("upload_id") != f.view.UploadID {
			f.t.Error("completion lost attempted upload")
		}
		checkpoint := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		f.view.CheckpointID, f.view.UploadStatus, f.view.Status = &checkpoint, "committed", "persisted"
		if f.capture.Unclean {
			f.view.Status = "persisted_unclean"
		}
	case "publish":
		if pending == nil || pending.record.Receipt == nil || pending.record.Receipt.CheckpointID == nil {
			f.t.Error("publication preceded durable persistence")
		}
	}
	if f.localFailure == phase {
		f.localFailure = ""
		if err := os.Rename(f.path, f.path+".retained"); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Mkdir(f.path, 0700); err != nil {
			f.t.Fatal(err)
		}
	}
	if f.fault == phase {
		f.fault = ""
		// The server has applied the operation; the client receives no usable acknowledgement.
		http.Error(w, "lost response", http.StatusServiceUnavailable)
		return
	}
	if phase == "termination" {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "committed": true, "status": "stopped", "data": termination})
		return
	}
	if phase == "reclaim" {
		if pending == nil || pending.record.Publication == nil || f.cleanupCalls == 0 {
			f.t.Error("reclamation preceded terminal publication or cleanup")
		}
		binding := f.capture.Binding
		verified := time.Now().UTC()
		authority := skillmanager.ReclamationAuthorization{Version: 1, RequestID: r.URL.Query().Get("request_id"),
			NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, SessionID: binding.SessionID,
			SnapshotID: binding.SnapshotID, FinalizationID: f.view.ID, CheckpointID: *f.view.CheckpointID,
			TreeDigest: f.capture.TreeDigest, Unclean: f.capture.Unclean, PublicationID: f.publication.ID,
			PublicationAttempt: f.publication.Attempt, PublicationStatus: f.publication.Status, VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "reclaimable", "committed": true, "retryable": false, "errors": []any{}, "data": authority})
		return
	}
	if phase == "publish" {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "committed": true, "status": f.publication.Status, "data": f.publication})
		return
	}
	if phase == "put" {
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "committed": false, "status": "upload_pending", "data": map[string]any{"upload_id": f.view.UploadID, "digest": f.manifest.Entries[0].SHA256, "created": true}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "committed": f.view.CheckpointID != nil, "status": f.view.Status, "data": f.view})
}

func (f *finalizationFixture) HoldSkillFinalization(_ context.Context, _ string, binding skillmanager.SnapshotBinding) (*os.File, skillmanager.FinalizationRecord, error) {
	if binding != f.capture.Binding {
		return nil, skillmanager.FinalizationRecord{}, errors.New("changed hold binding")
	}
	file, err := os.Open(f.object)
	if err == nil {
		f.holds = append(f.holds, file)
	}
	return file, f.capture, err
}

func (f *finalizationFixture) ReadSkillFinalization(_ context.Context, _ string, binding skillmanager.SnapshotBinding) (skillmanager.FinalizationRecord, skillmanager.Manifest, error) {
	if len(f.holds) == 0 {
		f.t.Fatal("manifest read without lifetime hold")
	}
	if _, err := f.holds[len(f.holds)-1].Stat(); err != nil {
		f.t.Fatal("hold closed before manifest read")
	}
	if binding != f.capture.Binding {
		f.t.Error("changed helper binding")
	}
	original := f.capture
	if f.fault == "helper_changed" {
		original.Unclean = !original.Unclean
	}
	return original, f.manifest, nil
}
func (f *finalizationFixture) OpenSkillFinalizationObject(_ context.Context, _ string, record skillmanager.FinalizationRecord, digest string) (*os.File, skillmanager.Entry, error) {
	if record != f.capture || digest != f.manifest.Entries[0].SHA256 {
		f.t.Error("changed object authority")
	}
	h, err := os.Open(f.object)
	if err == nil {
		f.handles = append(f.handles, h)
	}
	return h, f.manifest.Entries[0], err
}
func (f *finalizationFixture) ListSkillFinalizations(_ context.Context, _, nodeID, cursor string) (skillmanager.FinalizationPage, error) {
	if nodeID != f.capture.Binding.NodeID {
		f.t.Error("changed inventory node")
	}
	if f.page != nil {
		return *f.page, nil
	}
	return skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: f.capture.Binding.SessionID, Record: &f.capture}}}, nil
}

func (f *finalizationFixture) AcknowledgeSkillFinalization(_ context.Context, _ string, ack skillmanager.FinalizationAcknowledgement) (skillmanager.FinalizationRecord, error) {
	if !skillmanager.SameFinalizationInput(ack.Capture, f.capture) {
		f.t.Error("Helper acknowledgement changed original capture")
	}
	saved, err := f.journal.load(f.capture)
	if err != nil || saved == nil || saved.record.Receipt == nil || !reflect.DeepEqual(*saved.record.Receipt, ack.Receipt) || !reflect.DeepEqual(saved.record.Publication, ack.Publication) {
		f.t.Error("Helper acknowledgement preceded durable Server receipts", err)
	}
	state, err := ack.State()
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	record := f.capture
	record.State = state
	f.helperAcks = append(f.helperAcks, state)
	if f.helperFault == "ack:"+state {
		f.helperFault = ""
		return skillmanager.FinalizationRecord{}, errors.New("lost Helper acknowledgement")
	}
	return record, nil
}

func (f *finalizationFixture) CleanupFinalizedSkillSession(_ context.Context, _ string, capture skillmanager.FinalizationRecord) (skillmanager.FinalizationRecord, error) {
	saved, err := f.journal.load(capture)
	if err != nil || saved == nil || saved.record.Publication == nil || !capture.CanDeleteSession() || saved.record.Publication.Status != capture.State {
		f.t.Error("cleanup preceded durable terminal publication", err)
	}
	f.cleanupCalls++
	if f.helperFault == "cleanup" {
		f.helperFault = ""
		return skillmanager.FinalizationRecord{}, errors.New("lost cleanup response")
	}
	return capture, nil
}
func (f *finalizationFixture) transfer() error {
	return transferFinalization(context.Background(), f.client, f, f.journal, f.capture)
}

func TestFinalizationTransferSeparatesPersistenceAndPublication(t *testing.T) {
	for _, status := range []string{"published", "conflicted", "detached", "superseded"} {
		t.Run(status, func(t *testing.T) {
			f := newFinalizationFixture(t, status == "detached", status)
			err := f.transfer()
			if (err != nil) != (status == "superseded") {
				t.Fatal(err)
			}
			saved, err := f.journal.load(f.capture)
			if err != nil || saved == nil || saved.record.Publication.Status != status || saved.record.Receipt.CheckpointID == nil {
				t.Fatal("missing durable receipts", err)
			}
			if status == "published" && *saved.record.Receipt.CheckpointID == *saved.record.Publication.ResultCheckpointID {
				t.Fatal("incoming checkpoint replaced by merged result")
			}
			if !reflect.DeepEqual(f.recordedCalls(), []string{"termination", "begin", "put", "complete", "publish"}) || len(f.handles) != 1 {
				t.Fatal("failed digest deduplication or sequencing", f.recordedCalls())
			}
			f.reopen()
			_ = f.transfer()
			if len(f.recordedCalls()) != 5 {
				t.Fatal("completed decision was republished", f.recordedCalls())
			}
		})
	}
}

func TestFinalizationTransferRecoversLostAcknowledgementsAfterRestart(t *testing.T) {
	for _, phase := range []string{"termination", "begin", "put", "complete", "publish"} {
		t.Run(phase, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.fault = phase
			if err := f.transfer(); err == nil {
				t.Fatal("unknown response treated as success")
			}
			f.reopen()
			if err := f.transfer(); err != nil {
				t.Fatal("original transfer did not recover", err)
			}
			saved, err := f.journal.load(f.capture)
			if err != nil || saved.record.Publication == nil || saved.record.Capture.Binding.DirectoryEpoch != 9007199254740993 {
				t.Fatal("restart lost exact input", err)
			}
			want := map[string][]string{
				"termination": {"termination", "termination", "begin", "put", "complete", "publish"},
				"begin":       {"termination", "begin", "termination", "begin", "put", "complete", "publish"},
				"put":         {"termination", "begin", "put", "get", "begin", "put", "complete", "publish"},
				"complete":    {"termination", "begin", "put", "complete", "get", "publish"},
				"publish":     {"termination", "begin", "put", "complete", "publish", "get", "publish"},
			}
			if !reflect.DeepEqual(f.recordedCalls(), want[phase]) {
				t.Fatal("unsafe replay", f.recordedCalls())
			}
		})
	}
}

func TestFinalizationTransferRenewsOnlyOriginalExpiredInput(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	f.fault = "put"
	if err := f.transfer(); err == nil {
		t.Fatal("expected failure")
	}
	f.mu.Lock()
	f.view.UploadStatus = "expired"
	f.mu.Unlock()
	f.reopen()
	if err := f.transfer(); err != nil {
		t.Fatal(err)
	}
	saved, err := f.journal.load(f.capture)
	if err != nil || saved.record.Receipt.UploadAttempt != 2 || saved.record.Receipt.UploadID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatal("lost renewed attempt", err)
	}
}

func TestFinalizationTransferRetainsInputOnCorruptionAndLocalWriteFailure(t *testing.T) {
	for _, mode := range []string{"helper_changed", "object_changed", "journal_changed", "local_write", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "helper_changed":
				f.fault = "helper_changed"
			case "object_changed":
				f.corruptObject = true
				if err := os.WriteFile(f.object, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "journal_changed":
				if _, err := f.journal.save(nil, finalizationTransferRecord{SchemaVersion: 1, Capture: f.capture}); err != nil {
					t.Fatal(err)
				}
				f.capture.Unclean = true
			case "local_write":
				if err := os.Mkdir(f.path, 0700); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			if err := transferFinalization(ctx, f.client, f, f.journal, f.capture); err == nil {
				t.Fatal("invalid input accepted")
			}
			for _, phase := range f.recordedCalls() {
				if phase == "complete" || phase == "publish" {
					t.Fatal("failed input acknowledged")
				}
			}
			if _, err := os.Stat(f.object); err != nil {
				t.Fatal("failure removed original bytes", err)
			}
		})
	}
}

func TestFinalizationInventoryFailureDoesNotStarveLaterCapture(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	f.page = &skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{
		{SessionID: "33333333-3333-4333-8333-333333333333", Code: "invalid_retained_state"},
		{SessionID: f.capture.Binding.SessionID, Record: &f.capture},
	}}
	next, err := recoverFinalizationPage(context.Background(), f.client, f, f.journal, f.capture.Binding.NodeID, "")
	if !errors.Is(err, errFinalizationPending) || next != "" {
		t.Fatal("lost diagnostic", err, next)
	}
	saved, err := f.journal.load(f.capture)
	if err != nil || saved == nil || saved.record.Publication == nil {
		t.Fatal("corrupt earlier bundle starved valid capture", err)
	}
}

func TestFinalizationTransferRecoversServerCommitWhenLocalAcknowledgementFails(t *testing.T) {
	for _, phase := range []string{"begin", "complete", "publish"} {
		t.Run(phase, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.localFailure = phase
			if err := f.transfer(); err == nil {
				t.Fatal("failed local acknowledgement treated as durable")
			}
			if err := os.Remove(f.path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(f.path+".retained", f.path); err != nil {
				t.Fatal(err)
			}
			f.reopen()
			if err := f.transfer(); err != nil {
				t.Fatal("failed to recover verified Server commit", err)
			}
			saved, err := f.journal.load(f.capture)
			if err != nil || saved.record.Publication == nil {
				t.Fatal("recovery lost decision", err)
			}
			complete := 0
			for _, call := range f.recordedCalls() {
				if call == "complete" {
					complete++
				}
			}
			if complete != 1 {
				t.Fatal("known Server persistence repeated content completion", f.recordedCalls())
			}
		})
	}
}

func TestFinalizationJournalRejectsStaleAndChangedReceipts(t *testing.T) {
	f := newFinalizationFixture(t, false, "published")
	f.fault = "put"
	if err := f.transfer(); err == nil {
		t.Fatal("expected pending transfer")
	}
	previous, err := f.journal.load(f.capture)
	if err != nil {
		t.Fatal(err)
	}
	renewed := previous.record
	receipt := *renewed.Receipt
	receipt.UploadID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	receipt.UploadAttempt++
	renewed.Receipt = &receipt
	next, err := f.journal.save(previous, renewed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.save(previous, previous.record); !errors.Is(err, ledger.ErrConflict) {
		t.Fatal("stale observation overwrote new upload", err)
	}
	regressed := next.record
	regressed.Receipt = previous.record.Receipt
	if _, err := f.journal.save(next, regressed); err == nil {
		t.Fatal("new attempt downgraded")
	}
	changed := next.record
	changed.Capture.Binding.AccountID = changed.Capture.Binding.UserID
	if _, err := f.journal.save(next, changed); err == nil {
		t.Fatal("changed account accepted")
	}
	f.reopen()
	saved, err := f.journal.load(f.capture)
	if err != nil || saved.record.Receipt.UploadAttempt != 2 {
		t.Fatal("rejected mutations changed durable receipt", err)
	}
}

func (f *finalizationFixture) recordedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestFinalizationWorkerRecoversLostHelperAcknowledgementAndCleanup(t *testing.T) {
	for _, phase := range []string{"ack:upload_pending", "ack:persisted", "ack:published", "cleanup"} {
		t.Run(phase, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.helperFault = phase
			if err := f.transfer(); err == nil {
				t.Fatal("lost Helper response treated as completed")
			}
			before := f.recordedCalls()
			f.reopen()
			if err := f.transfer(); err != nil {
				t.Fatal("durable original receipts failed to resume Helper acknowledgement", err)
			}
			if phase == "ack:published" || phase == "cleanup" {
				if !reflect.DeepEqual(before, f.recordedCalls()) {
					t.Fatal("Helper retry republished Server data", f.recordedCalls())
				}
			}
			if f.cleanupCalls == 0 || len(f.helperAcks) == 0 || f.helperAcks[len(f.helperAcks)-1] != "published" {
				t.Fatal("Helper retention did not precede cleanup")
			}
			if _, err := os.Stat(f.object); err != nil {
				t.Fatal("retry lost frozen input", err)
			}
		})
	}
}

func TestFinalizationTransferReleasesReadHoldOnSuccessAndFailure(t *testing.T) {
	for _, phase := range []string{"", "begin", "put", "complete", "publish"} {
		t.Run(phase, func(t *testing.T) {
			f := newFinalizationFixture(t, false, "published")
			f.fault = phase
			err := f.transfer()
			if (err != nil) != (phase != "") {
				t.Fatal("unexpected transfer outcome", err)
			}
			if len(f.holds) != 1 {
				t.Fatal("transfer did not acquire one complete read lifetime")
			}
			if _, err := f.holds[0].Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("transfer leaked content read hold", err)
			}
		})
	}
}

type fixtureFrozenObjects struct {
	fixture *finalizationFixture
	capture skillmanager.FinalizationRecord
}

func (f *finalizationFixture) OpenSkillFinalizationObjects(_ context.Context, _ string, capture skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error) {
	return &fixtureFrozenObjects{f, capture}, nil
}

func (r *fixtureFrozenObjects) Open(ctx context.Context, digest string) (*os.File, skillmanager.Entry, error) {
	return r.fixture.OpenSkillFinalizationObject(ctx, "fixture-object", r.capture, digest)
}

func (r *fixtureFrozenObjects) Close() error { return nil }
