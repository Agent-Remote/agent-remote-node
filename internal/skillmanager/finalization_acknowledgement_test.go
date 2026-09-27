package skillmanager

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func finalizationAckFixture(capture FinalizationRecord, target string) FinalizationAcknowledgement {
	receipt := FinalizationReceipt{ID: "66666666-6666-4666-8666-666666666666", SnapshotID: capture.Binding.SnapshotID, IncomingDigest: capture.TreeDigest, Unclean: capture.Unclean, Status: "upload_pending", UploadID: "77777777-7777-4777-8777-777777777777", UploadAttempt: 1, UploadStatus: "staged", ExpiresAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	ack := FinalizationAcknowledgement{Version: 1, Capture: capture, Receipt: receipt}
	if target == "upload_pending" {
		return ack
	}
	checkpoint := "88888888-8888-4888-8888-888888888888"
	ack.Receipt.CheckpointID, ack.Receipt.UploadStatus, ack.Receipt.Status = &checkpoint, "committed", "persisted"
	if capture.Unclean {
		ack.Receipt.Status = "persisted_unclean"
	}
	if target == "persisted" || target == "persisted_unclean" {
		return ack
	}
	ack.Publication = &PublicationReceipt{ID: "99999999-9999-4999-8999-999999999999", FinalizationID: receipt.ID, Attempt: 1, Status: target}
	switch target {
	case "published":
		merged := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		ack.Publication.ResultCheckpointID = &merged
	case "conflicted":
		ack.Publication.ConflictCount = 2
	case "detached":
		reason := "directory_epoch_changed"
		if capture.Unclean {
			reason = "unclean"
		}
		ack.Publication.Reason = &reason
	case "superseded":
		reason := "head_changed"
		ack.Publication.Reason = &reason
	}
	return ack
}

func TestFinalizationAcknowledgementRequiresCanonicalReceipts(t *testing.T) {
	capture := FinalizationRecord{Version: 1, ObjectsVersion: 1, State: "local_durable", TreeDigest: strings.Repeat("a", 64), Binding: SnapshotBinding{NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", AccountID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444", SnapshotID: "55555555-5555-4555-8555-555555555555", DirectoryEpoch: 9007199254740993, LibraryGeneration: 0, InitialTreeDigest: strings.Repeat("b", 64)}}
	ack := finalizationAckFixture(capture, "published")
	data, _ := json.Marshal(ack)
	mutations := map[string]string{
		"missing_publication": strings.Replace(string(data), `,"publication":`+mustAckJSON(t, ack.Publication), "", 1),
		"null_capture":        strings.Replace(string(data), `"capture":`+mustAckJSON(t, ack.Capture), `"capture":null`, 1),
		"duplicate_version":   strings.Replace(string(data), `"version":1`, `"version":1,"version":1`, 1),
		"aliased_receipt":     strings.Replace(string(data), `"receipt":`, `"Receipt":`, 1),
		"missing_unclean":     strings.Replace(string(data), `"unclean":false,`, "", 1),
		"null_checkpoint":     strings.Replace(string(data), `"checkpoint_id":"88888888-8888-4888-8888-888888888888"`, `"checkpoint_id":null`, 1),
	}
	for name, payload := range mutations {
		t.Run(name, func(t *testing.T) {
			var got FinalizationAcknowledgement
			if err := json.Unmarshal([]byte(payload), &got); err == nil && got.Validate() == nil {
				t.Fatal("malformed authority accepted")
			}
		})
	}
	var got FinalizationAcknowledgement
	if err := json.Unmarshal(data, &got); err != nil || got.Validate() != nil || got.Capture.Binding.DirectoryEpoch != 9007199254740993 {
		t.Fatal("valid receipt changed", err)
	}
}

func mustAckJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
