package runtimehelper

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperFinalizationInventoryContinuesPastBrokenBundles(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	root := engine.config.SkillStateRoot
	for i := 1; i <= skillmanager.FinalizationPageLimit+1; i++ {
		id := fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)
		if err := os.Mkdir(filepath.Join(root, "session-"+id), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/missing", filepath.Join(root, "session-ffffffff-ffff-4fff-8fff-ffffffffffff")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "session-malformed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "session-"+record.Binding.SessionID, "work")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(engine.config.StateRoot); err != nil {
		t.Fatal(err)
	}
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	cursor := ""
	found, invalid := 0, 0
	for {
		page, err := client.ListSkillFinalizations(context.Background(), "inventory", record.Binding.NodeID, cursor)
		if err != nil || !page.InvalidNames {
			t.Fatal("inventory failed", err)
		}
		for _, item := range page.Items {
			if item.Record != nil {
				if *item.Record != record {
					t.Fatal("inventory changed capture")
				}
				found++
			} else {
				if item.Code != "invalid_retained_state" {
					t.Fatal("corruption treated as absence", item.Code)
				}
				invalid++
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if found != 1 || invalid != skillmanager.FinalizationPageLimit+2 {
		t.Fatal("bad inventory coverage", found, invalid)
	}
	if _, err := client.ListSkillFinalizations(context.Background(), "foreign", record.Binding.AccountID, ""); err == nil {
		t.Fatal("foreign node received inventory")
	}
}

func TestHelperFinalizationInventoryPreservesLargeGenerations(t *testing.T) {
	_, record := finalizationTransferFixture(t)
	record.Binding.DirectoryEpoch = 9007199254740993
	record.Binding.LibraryGeneration = 9007199254740995
	page := skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{{SessionID: record.Binding.SessionID, Record: &record}}}
	client, stop := fakeCapturePeer(t, func(conn *net.UnixConn) {
		mapped, err := Map(page)
		if err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(conn).Encode(Response{Version: 1, OK: true, Result: mapped})
	})
	defer stop()
	got, err := client.ListSkillFinalizations(context.Background(), "large-generation", record.Binding.NodeID, "")
	if err != nil || len(got.Items) != 1 || got.Items[0].Record == nil || *got.Items[0].Record != record {
		t.Fatal("inventory rounded original generation", err)
	}
}

func TestHelperFinalizationInventoryCannotTurnCorruptionIntoPending(t *testing.T) {
	for _, mode := range []string{"record_missing", "manifest_missing", "linked_finalization", "not_finalized", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			engine, record := finalizationTransferFixture(t)
			root := filepath.Join(engine.config.SkillStateRoot, "session-"+record.Binding.SessionID, "finalization")
			switch mode {
			case "record_missing":
				if err := os.Remove(filepath.Join(root, "record.json")); err != nil {
					t.Fatal(err)
				}
			case "manifest_missing":
				if err := os.Remove(filepath.Join(root, "manifest.json")); err != nil {
					t.Fatal(err)
				}
			case "linked_finalization":
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/missing", root); err != nil {
					t.Fatal(err)
				}
			case "not_finalized":
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				record.ObjectsVersion = 0
				data, _ := json.Marshal(record)
				if err := os.WriteFile(filepath.Join(root, "record.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			page, err := client.ListSkillFinalizations(context.Background(), "inspect", record.Binding.NodeID, "")
			if err != nil || len(page.Items) != 1 {
				t.Fatal("lost retained candidate", err)
			}
			item := page.Items[0]
			want := "invalid_retained_state"
			if mode == "not_finalized" {
				want = "not_finalized"
			}
			if mode == "legacy" {
				want = ""
				if item.Record == nil || item.Record.ObjectsVersion != 0 {
					t.Fatal("read-only scan upgraded legacy journal")
				}
			}
			if item.Code != want {
				t.Fatal("wrong diagnostic", item.Code, want)
			}
		})
	}
}

func TestHelperFinalizationInventoryCancelsLockWait(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	file, err := os.CreateTemp("", "finalization-list-")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(path)
	server := NewServer(path, -1, os.Getuid(), engine)
	server.mu.Lock()
	defer server.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err == nil {
			server.handle(context.Background(), conn)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	if _, err := NewClient(path).ListSkillFinalizations(ctx, "blocked", record.Binding.NodeID, ""); err == nil {
		t.Fatal("cancelled inventory succeeded")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled scan retained blocked handler")
	}
}

func TestHelperFinalizationFrozenInspectReadsOnlyExistingExactFinalization(t *testing.T) {
	for _, fault := range []string{"", "foreign_node", "foreign_session", "missing_store", "not_frozen", "linked_record"} {
		t.Run(fault, func(t *testing.T) {
			engine, record := finalizationTransferFixture(t)
			node, session := record.Binding.NodeID, record.Binding.SessionID
			finalization := filepath.Join(engine.config.SkillStateRoot, "session-"+session, "finalization")
			before, err := os.ReadFile(filepath.Join(finalization, "record.json"))
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "foreign_node":
				node = record.Binding.UserID
			case "foreign_session":
				session = record.Binding.UserID
			case "missing_store":
				if err := os.RemoveAll(engine.config.SkillStateRoot); err != nil {
					t.Fatal(err)
				}
			case "not_frozen":
				if err := os.RemoveAll(finalization); err != nil {
					t.Fatal(err)
				}
			case "linked_record":
				path := filepath.Join(finalization, "record.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/missing", path); err != nil {
					t.Fatal(err)
				}
			}
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			got, err := client.InspectSkillFinalization(context.Background(), "frozen-inspect", node, session)
			if (err != nil) != (fault != "") {
				t.Fatal("wrong frozen inspection result", err)
			}
			if fault == "" {
				after, err := os.ReadFile(filepath.Join(finalization, "record.json"))
				if err != nil || string(before) != string(after) || got != record {
					t.Fatal("read changed frozen state", err)
				}
			}
			if fault == "missing_store" || fault == "not_frozen" {
				path := finalization
				if fault == "missing_store" {
					path = engine.config.SkillStateRoot
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("inspection created or froze absent state", err)
				}
			}
		})
	}
}

func TestHelperFinalizationInventoryDistinguishesReclamationFromCorruption(t *testing.T) {
	engine, capture := finalizationTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	ack := helperFinalizationAck(capture, "published")
	capture, err := client.AcknowledgeSkillFinalization(context.Background(), "ack-reclamation", ack)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CleanupFinalizedSkillSession(context.Background(), "cleanup-reclamation", capture); err != nil {
		t.Fatal(err)
	}
	bundle, _, err := engine.retainedNativeSkillSession(capture.Binding.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	binding := capture.Binding
	verified := time.Now()
	authority := skillmanager.ReclamationAuthorization{Version: 1, RequestID: binding.SnapshotID,
		NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID,
		SessionID: binding.SessionID, SnapshotID: binding.SnapshotID, FinalizationID: ack.Receipt.ID,
		CheckpointID: *ack.Receipt.CheckpointID, TreeDigest: capture.TreeDigest,
		PublicationID: ack.Publication.ID, PublicationAttempt: ack.Publication.Attempt,
		PublicationStatus: ack.Publication.Status, VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
	root := filepath.Join(engine.config.StateRoot, "sessions", binding.SessionID)
	intent, err := skillmanager.MarkFinalizationReclamation(context.Background(), bundle, capture, authority, time.Now().Add(time.Minute), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"reclamation_pending", "content_reclaimed", "invalid_retained_state"} {
		if code == "content_reclaimed" {
			// Model interrupted deletion separately from the socket's read-only inventory contract.
			for _, name := range []string{"work", "finalization/objects"} {
				if err := bundle.RemoveAll(name); err != nil {
					t.Fatal(err)
				}
			}
			if err := skillmanager.RetainFinalizationReclaimed(context.Background(), bundle, intent); err != nil {
				t.Fatal(err)
			}
		}
		if code == "invalid_retained_state" {
			if err := bundle.Remove("finalization/reclamation.json"); err != nil {
				t.Fatal(err)
			}
		}
		page, err := client.ListSkillFinalizations(context.Background(), "inventory-reclamation", binding.NodeID, "")
		if err != nil || len(page.Items) != 1 || page.Items[0].Record != nil || page.Items[0].Code != code {
			t.Fatal("reclamation was confused with a new or empty capture", code, page, err)
		}
		if _, _, err := client.ReadSkillFinalization(context.Background(), "export-reclamation", binding); err == nil {
			t.Fatal("reclamation produced a complete empty export")
		}
	}
}
