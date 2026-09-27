package runtimehelper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func helperFinalizationAck(capture skillmanager.FinalizationRecord, target string) skillmanager.FinalizationAcknowledgement {
	receipt := skillmanager.FinalizationReceipt{ID: "66666666-6666-4666-8666-666666666666", SnapshotID: capture.Binding.SnapshotID, IncomingDigest: capture.TreeDigest, Unclean: capture.Unclean, Status: "upload_pending", UploadID: "77777777-7777-4777-8777-777777777777", UploadAttempt: 1, UploadStatus: "staged", ExpiresAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	ack := skillmanager.FinalizationAcknowledgement{Version: 1, Capture: capture, Receipt: receipt}
	if target == "upload_pending" {
		return ack
	}
	checkpoint := "88888888-8888-4888-8888-888888888888"
	ack.Receipt.CheckpointID, ack.Receipt.UploadStatus, ack.Receipt.Status = &checkpoint, "committed", "persisted"
	if capture.Unclean {
		ack.Receipt.Status = "persisted_unclean"
	}
	if target == "persisted" {
		return ack
	}
	ack.Publication = &skillmanager.PublicationReceipt{ID: "99999999-9999-4999-8999-999999999999", FinalizationID: receipt.ID, Attempt: 1, Status: target}
	switch target {
	case "published":
		merged := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		ack.Publication.ResultCheckpointID = &merged
	case "conflicted":
		ack.Publication.ConflictCount = 1
	case "detached":
		reason := "unclean"
		if !capture.Unclean {
			reason = "directory_epoch_changed"
		}
		ack.Publication.Reason = &reason
	case "superseded":
		reason := "head_changed"
		ack.Publication.Reason = &reason
	}
	return ack
}

func TestHelperFinalizationAcknowledgementAndCleanupPreserveFrozenInput(t *testing.T) {
	for _, target := range []string{"published", "conflicted", "detached", "superseded"} {
		t.Run(target, func(t *testing.T) {
			engine, capture := finalizationTransferFixture(t)
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			root := filepath.Join(engine.config.StateRoot, "sessions", capture.Binding.SessionID)
			forged := capture
			forged.State = "published"
			if _, err := client.CleanupFinalizedSkillSession(context.Background(), "premature", forged); err == nil {
				t.Fatal("caller-supplied terminal state authorized cleanup")
			}
			for _, phase := range []string{"upload_pending", "persisted", target} {
				ack := helperFinalizationAck(capture, phase)
				got, err := client.AcknowledgeSkillFinalization(context.Background(), "ack", ack)
				state, _ := ack.State()
				if err != nil || got.State != state {
					t.Fatal("failed exact acknowledgement", err, got.State)
				}
				if _, err := os.Stat(root); err != nil {
					t.Fatal("acknowledgement removed transient resources", err)
				}
				if phase != target {
					continue
				}
				if target == "superseded" {
					if got.CanDeleteSession() {
						t.Fatal("superseded authorized cleanup")
					}
					return
				}
				if _, err := client.CleanupFinalizedSkillSession(context.Background(), "cleanup", got); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("transient runtime root remains", err)
				}
				if _, err := client.CleanupFinalizedSkillSession(context.Background(), "replay", got); err != nil {
					t.Fatal("cleanup replay failed", err)
				}
				bundle, _, err := engine.retainedNativeSkillSession(capture.Binding.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				defer bundle.Close()
				if _, err := bundle.Stat("work/retained"); err != nil {
					t.Fatal("cleanup removed durable work", err)
				}
				if err := bundle.RemoveAll("work"); err != nil {
					t.Fatal(err)
				}
				_, manifest, err := client.ReadSkillFinalization(context.Background(), "frozen", capture.Binding)
				if err != nil || len(manifest.Entries) != 1 {
					t.Fatal("cleanup lost original frozen bytes", err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(root, "new-resource")
				if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := client.CleanupFinalizedSkillSession(context.Background(), "reappeared", got); err == nil {
					t.Fatal("historical cleanup adopted new runtime root")
				}
				if _, err := os.Stat(sentinel); err != nil {
					t.Fatal("new resource removed by replay", err)
				}
			}
		})
	}
}

func TestHelperFinalizationCleanupRefusesWritersAndChangedRuntime(t *testing.T) {
	for _, mode := range []string{"active", "populated", "foreign_node", "changed_spec", "cross_boot", "network_unknown", "tmp_link"} {
		t.Run(mode, func(t *testing.T) {
			engine, capture := finalizationTransferFixture(t)
			original := engine
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			confirmed, err := client.AcknowledgeSkillFinalization(context.Background(), "ack", helperFinalizationAck(capture, "published"))
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(engine.config.StateRoot, "sessions", capture.Binding.SessionID)
			switch mode {
			case "active":
				if err := os.WriteFile(os.Getenv("UNIT_STATE"), []byte(unitFields("active", "/system.slice/agent-remote-session-"+shortDigest(capture.Binding.SessionID, 12)+".service", "success", "0", "0")), 0600); err != nil {
					t.Fatal(err)
				}
			case "populated":
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", "agent-remote-session-"+shortDigest(capture.Binding.SessionID, 12)+".service")
				if err := os.MkdirAll(group, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign_node":
				confirmed.Binding.NodeID = confirmed.Binding.UserID
			case "changed_spec":
				spec, err := engine.loadSpec(capture.Binding.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				spec.RuntimeUID++
				data, _ := json.Marshal(spec)
				if err := os.WriteFile(engine.specPath(spec.SessionID), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "cross_boot":
				path := filepath.Join(engine.config.SkillStateRoot, "session-"+capture.Binding.SessionID, "runtime.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var binding skillmanager.RuntimeBinding
				if err := json.Unmarshal(data, &binding); err != nil {
					t.Fatal(err)
				}
				binding.BootID = capture.Binding.UserID
				data, _ = json.Marshal(binding)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "network_unknown":
				engine.config.IPPath = writeTestCommand(t, "failed-ip", "exit 1")
			case "tmp_link":
				if err := os.Symlink("/tmp", filepath.Join(root, "tmp")); err != nil {
					t.Fatal(err)
				}
			}
			mapped, _ := Map(finalizationCleanupRequest{Capture: confirmed})
			if _, err := engine.cleanupFinalization(context.Background(), Request{Version: 1, RequestID: "guarded-cleanup", Operation: finalizationCleanupOperation, Payload: mapped}); err == nil {
				t.Fatal("unsafe cleanup succeeded")
			}
			if _, err := os.Stat(original.specPath(capture.Binding.SessionID)); err != nil {
				t.Fatal("cleanup failure removed original spec", err)
			}
		})
	}
}
