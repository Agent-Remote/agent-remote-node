package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type captureInitiatorStub struct {
	captureReaderStub
	capture func(context.Context, string, skillmanager.AccountTakeoverBinding, []skillmanager.AccountWriter) (skillmanager.AccountCapture, error)
}

func (s captureInitiatorStub) CaptureAccountTakeover(ctx context.Context, id string, b skillmanager.AccountTakeoverBinding, inventory []skillmanager.AccountWriter) (skillmanager.AccountCapture, error) {
	return s.capture(ctx, id, b, inventory)
}

func takeoverEnvelope(b skillmanager.AccountTakeoverBinding) api.TaskEnvelope {
	id := "takeover_tool_account_skills:" + b.TakeoverID
	return api.TaskEnvelope{TaskID: id, TaskRecordID: b.TaskID, TaskType: "takeover_tool_account_skills", NodeID: b.NodeID, IdempotencyKey: id, LeaseAttempt: 3, Payload: map[string]any{
		"takeover_id": b.TakeoverID, "user_id": b.UserID, "tool_account_id": b.AccountID, "runtime_backend": b.RuntimeBackend, "directory_epoch": b.DirectoryEpoch, "inventory_digest": b.InventoryDigest, "protocol_version": 1, "manifest_version": 1,
	}}
}

func TestTakeoverInitiationCoversCaptureAndTransferWithOneLease(t *testing.T) {
	f := newTransferFixture(t)
	var renewals atomic.Int32
	renewed := make(chan struct{})
	f.client.takeoverLeaseFunc = func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
		if renewals.Add(1) == 2 {
			close(renewed)
		}
		return shortTakeoverLease(), nil
	}
	helper := captureInitiatorStub{captureReaderStub: f.helper, capture: func(ctx context.Context, id string, binding skillmanager.AccountTakeoverBinding, inventory []skillmanager.AccountWriter) (skillmanager.AccountCapture, error) {
		if renewals.Load() == 0 || id != "takeover-capture:"+binding.TaskID || binding != f.capture.Binding || len(inventory) != 0 {
			t.Error("capture lacks original lease and inventory")
		}
		select {
		case <-renewed:
		case <-ctx.Done():
			return skillmanager.AccountCapture{}, ctx.Err()
		}
		f.events = append(f.events, "capture")
		return f.capture, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := initiateTakeover(ctx, f.client, helper, f.capture.Binding, 3)
	if err != nil || result.CheckpointID != f.receipt.CheckpointID {
		t.Fatal(result, err)
	}
	if !reflect.DeepEqual(f.events, []string{"get", "capture", "read", "begin", "open", "put", "complete"}) {
		t.Fatal(f.events)
	}
}

func TestTakeoverInitiationLeaseLossCancelsCaptureBeforeUpload(t *testing.T) {
	f := newTransferFixture(t)
	var renewals atomic.Int32
	f.client.takeoverLeaseFunc = func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
		if renewals.Add(1) > 1 {
			return api.SkillTakeoverLease{}, errors.New("denied")
		}
		return shortTakeoverLease(), nil
	}
	helper := captureInitiatorStub{capture: func(ctx context.Context, _ string, _ skillmanager.AccountTakeoverBinding, _ []skillmanager.AccountWriter) (skillmanager.AccountCapture, error) {
		<-ctx.Done()
		return skillmanager.AccountCapture{}, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := initiateTakeover(ctx, f.client, helper, f.capture.Binding, 3)
	if !errors.Is(err, errTakeoverLeaseLost) || !reflect.DeepEqual(f.events, []string{"get"}) {
		t.Fatal(err, f.events)
	}
}

func TestTakeoverDispatchStrictIdentityAndNoFailureCache(t *testing.T) {
	f := newTransferFixture(t)
	for _, change := range []string{"node", "record", "logical", "key", "attempt", "backend", "version", "case", "null", "fraction", "path", "unavailable"} {
		t.Run(change, func(t *testing.T) {
			task := takeoverEnvelope(f.capture.Binding)
			switch change {
			case "node":
				task.NodeID = f.capture.Binding.UserID
			case "record":
				task.TaskRecordID = "bad"
			case "logical":
				task.TaskID = "changed"
			case "key":
				task.IdempotencyKey = "changed"
			case "attempt":
				task.LeaseAttempt = 0
			case "backend":
				task.Payload["runtime_backend"] = "docker_sandbox"
			case "version":
				task.Payload["manifest_version"] = 2
			case "case":
				task.Payload["User_ID"] = task.Payload["user_id"]
				delete(task.Payload, "user_id")
			case "null":
				task.Payload["directory_epoch"] = nil
			case "fraction":
				task.Payload["directory_epoch"] = 1.1
			case "path":
				task.Payload["path"] = "/tmp/source"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if change != "unavailable" || r.Method != "GET" {
					t.Error("invalid task reached network mutation", r.URL.Path)
				}
				http.Error(w, "denied", http.StatusForbidden)
			}))
			defer server.Close()
			journal, err := ledger.Open(filepath.Join(t.TempDir(), "ledger"))
			if err != nil {
				t.Fatal(err)
			}
			worker := New(config.Config{NodeID: f.capture.Binding.NodeID, AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "test"), journal)
			if err := worker.executeTask(context.Background(), task); !errors.Is(err, errTakeoverPending) {
				t.Fatal(err)
			}
			if _, exists, err := journal.Get(task.TaskID); exists || err != nil {
				t.Fatal("failure cached", err)
			}
		})
	}
}

func TestTakeoverDispatchCommittedReplayUsesServerEvenWithLegacyCache(t *testing.T) {
	f := newTransferFixture(t)
	b := f.capture.Binding
	task := takeoverEnvelope(b)
	receipt := f.receipt
	receipt.ProtocolVersion, receipt.ManifestVersion = 1, 1
	receipt.TakeoverID, receipt.TaskID, receipt.NodeID, receipt.UserID, receipt.AccountID = b.TakeoverID, b.TaskID, b.NodeID, b.UserID, b.AccountID
	receipt.RuntimeBackend, receipt.DirectoryEpoch, receipt.InventoryDigest = b.RuntimeBackend, b.DirectoryEpoch, b.InventoryDigest
	receipt.Inventory = []skillmanager.AccountWriter{}
	receipt.HelperReceiptID, receipt.CaptureDigest, receipt.UploadAttempt = &f.capture.HelperReceiptID, &f.capture.TreeDigest, 1
	var completes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "committed", "committed": true, "data": receipt})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/complete") {
			t.Error("unexpected mutation", r.URL.Path)
		}
		var data struct {
			Result map[string]any `json:"result"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil || data.Result["checkpoint_id"] != *receipt.CheckpointID || len(data.Result) != 6 {
			t.Error("wrong result", data, err)
		}
		completes++
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()
	journal, err := ledger.Open(filepath.Join(t.TempDir(), "ledger"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Save(ledger.Entry{TaskID: task.TaskID, Status: "failed", Error: map[string]any{"code": "old"}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		worker := New(config.Config{NodeID: b.NodeID, RuntimeSocketPath: "/missing/helper", AllowedRuntimeBackends: []string{"native"}}, api.NewClient(server.URL, "test"), journal)
		if err := worker.executeTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	if completes != 2 {
		t.Fatal(completes)
	}
}
