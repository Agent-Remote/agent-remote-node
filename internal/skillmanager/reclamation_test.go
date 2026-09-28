package skillmanager

import (
	"strings"
	"testing"
	"time"
)

func TestReclamationRequiresTerminalInputWhileAllowingLaterPublication(t *testing.T) {
	for _, outcome := range []string{"published", "conflicted", "detached", "unclean", "persisted", "superseded"} {
		t.Run(outcome, func(t *testing.T) {
			capture := reclamationCaptureFixture()
			target := outcome
			if outcome == "unclean" {
				capture.Unclean, target = true, "detached"
			}
			ack := finalizationAckFixture(capture, target)
			verified := time.Now()
			authority := ReclamationAuthorization{Version: 1, RequestID: capture.Binding.SnapshotID,
				NodeID: capture.Binding.NodeID, UserID: capture.Binding.UserID, AccountID: capture.Binding.AccountID,
				SessionID: capture.Binding.SessionID, SnapshotID: capture.Binding.SnapshotID, FinalizationID: ack.Receipt.ID,
				CheckpointID: *ack.Receipt.CheckpointID, TreeDigest: capture.TreeDigest, Unclean: capture.Unclean,
				PublicationID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", PublicationAttempt: 2,
				PublicationStatus: "detached", VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
			want := outcome != "persisted" && outcome != "superseded"
			if err := authority.Match(ack); (err == nil) != want {
				t.Fatal("reclamation confused remote publication with original input", err)
			}
			if want {
				authority.CheckpointID = capture.Binding.SnapshotID
				if err := authority.Match(ack); err == nil {
					t.Fatal("later publication replaced original input checkpoint")
				}
			}
		})
	}
}

func TestReclamationObservationCannotRewriteSamePublicationDecision(t *testing.T) {
	capture := reclamationCaptureFixture()
	ack := finalizationAckFixture(capture, "published")
	verified := time.Now()
	a := ReclamationAuthorization{Version: 1, RequestID: capture.Binding.SnapshotID,
		NodeID: capture.Binding.NodeID, UserID: capture.Binding.UserID, AccountID: capture.Binding.AccountID,
		SessionID: capture.Binding.SessionID, SnapshotID: capture.Binding.SnapshotID,
		FinalizationID: ack.Receipt.ID, CheckpointID: *ack.Receipt.CheckpointID, TreeDigest: capture.TreeDigest,
		PublicationID: ack.Publication.ID, PublicationAttempt: ack.Publication.Attempt,
		PublicationStatus: "detached", VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
	if err := a.Match(ack); err == nil {
		t.Fatal("same publication identity changed its terminal decision")
	}
}

func reclamationCaptureFixture() FinalizationRecord {
	return FinalizationRecord{Version: 1, ObjectsVersion: 1, State: "local_durable", TreeDigest: strings.Repeat("a", 64),
		Binding: SnapshotBinding{NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222",
			AccountID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444",
			SnapshotID: "55555555-5555-4555-8555-555555555555", DirectoryEpoch: 1, InitialTreeDigest: strings.Repeat("b", 64)}}
}

func TestReclamationAcceptsResolvedOriginalConflict(t *testing.T) {
	capture := reclamationCaptureFixture()
	ack := finalizationAckFixture(capture, "conflicted")
	now := time.Now()
	authority := ReclamationAuthorization{Version: 1, RequestID: capture.Binding.SnapshotID,
		NodeID: capture.Binding.NodeID, UserID: capture.Binding.UserID, AccountID: capture.Binding.AccountID,
		SessionID: capture.Binding.SessionID, SnapshotID: capture.Binding.SnapshotID, FinalizationID: ack.Receipt.ID,
		CheckpointID: *ack.Receipt.CheckpointID, TreeDigest: capture.TreeDigest,
		PublicationID: ack.Publication.ID, PublicationAttempt: ack.Publication.Attempt,
		PublicationStatus: "published", VerifiedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := authority.Match(ack); err != nil {
		t.Fatal("resolved conflict could not reclaim its original input", err)
	}
	authority.TreeDigest = strings.Repeat("c", 64)
	if err := authority.Match(ack); err == nil {
		t.Fatal("conflict resolution replaced the original input")
	}
}
