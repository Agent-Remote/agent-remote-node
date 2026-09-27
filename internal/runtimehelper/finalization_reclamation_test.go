package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func reclamationProtocolFixture() (skillmanager.FinalizationRecord, skillmanager.FinalizationAcknowledgement, skillmanager.ReclamationAuthorization) {
	digest := strings.Repeat("a", 64)
	capture := skillmanager.FinalizationRecord{Version: 1, ObjectsVersion: 1, State: "published", TreeDigest: digest,
		Binding: skillmanager.SnapshotBinding{NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222",
			AccountID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444",
			SnapshotID: "55555555-5555-4555-8555-555555555555", DirectoryEpoch: 9007199254740993, LibraryGeneration: 9007199254740995, InitialTreeDigest: digest}}
	checkpoint := "88888888-8888-4888-8888-888888888888"
	ack := skillmanager.FinalizationAcknowledgement{Version: 1, Capture: capture,
		Receipt: skillmanager.FinalizationReceipt{ID: "66666666-6666-4666-8666-666666666666", SnapshotID: capture.Binding.SnapshotID,
			IncomingDigest: digest, Status: "persisted", CheckpointID: &checkpoint, UploadID: "77777777-7777-4777-8777-777777777777",
			UploadAttempt: 1, UploadStatus: "committed", ExpiresAt: time.Now().UTC().Add(time.Hour)},
		Publication: &skillmanager.PublicationReceipt{ID: "99999999-9999-4999-8999-999999999999", FinalizationID: "66666666-6666-4666-8666-666666666666",
			Attempt: 1, Status: "published", ResultCheckpointID: &checkpoint}}
	verified := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	authority := skillmanager.ReclamationAuthorization{Version: 1, RequestID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		NodeID: capture.Binding.NodeID, UserID: capture.Binding.UserID, AccountID: capture.Binding.AccountID, SessionID: capture.Binding.SessionID,
		SnapshotID: capture.Binding.SnapshotID, FinalizationID: ack.Receipt.ID, CheckpointID: checkpoint, TreeDigest: digest,
		PublicationID: ack.Publication.ID, PublicationAttempt: 1, PublicationStatus: "published", VerifiedAt: verified, ExpiresAt: verified.Add(time.Minute)}
	return capture, ack, authority
}

func TestReclamationFramesRejectAmbiguousFields(t *testing.T) {
	capture, ack, _ := reclamationProtocolFixture()
	for _, frame := range []finalizationReclamationFrame{
		{Version: 1, Kind: "authorize", Challenge: capture.Binding.SnapshotID, Acknowledgement: &ack},
		{Version: 1, Kind: "reclaimed", Record: &capture}, {Version: 1, Kind: "pending"},
	} {
		encoded, _ := json.Marshal(frame)
		if _, err := readReclamationFrame(bufio.NewReader(bytes.NewReader(append(encoded, '\n')))); err != nil {
			t.Fatal("valid fixture rejected", err)
		}
		var original map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &original)
		for name := range original {
			for _, change := range []string{"missing", "null", "duplicate", "alias"} {
				t.Run(frame.Kind+"/"+name+"/"+change, func(t *testing.T) {
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(encoded, &fields)
					switch change {
					case "missing":
						delete(fields, name)
					case "null":
						fields[name] = json.RawMessage("null")
					case "alias":
						fields[strings.ToUpper(name)] = fields[name]
						delete(fields, name)
					}
					data, _ := json.Marshal(fields)
					if change == "duplicate" {
						data = append([]byte(`{"`+name+`":`+string(fields[name])+`,`), data[1:]...)
					}
					if _, err := readReclamationFrame(bufio.NewReader(bytes.NewReader(append(data, '\n')))); err == nil {
						t.Fatal("ambiguous frame accepted")
					}
				})
			}
		}
	}
	for _, input := range []string{"{}\n", "{\"version\":1,\"kind\":\"pending\",\"path\":\"/untrusted\"}\n", strings.Repeat("x", maxReclamationFrameBytes+1) + "\n"} {
		if _, err := readReclamationFrame(bufio.NewReader(strings.NewReader(input))); err == nil {
			t.Fatal("unbounded or unknown frame accepted")
		}
	}
}

type delayedReclamationWriter struct {
	writer io.Writer
	delay  time.Duration
}

func (w delayedReclamationWriter) Write(data []byte) (int, error) {
	time.Sleep(w.delay)
	return w.writer.Write(data)
}

func TestReclamationChallengeChargesBothIPCAndRemoteDelay(t *testing.T) {
	for _, tc := range []struct {
		name         string
		write, reply time.Duration
		stale        bool
	}{{"fresh", 5 * time.Second, 20 * time.Second, false}, {"expired", 30 * time.Second, 31 * time.Second, false}, {"boundary", 0, time.Minute, false}, {"replayed", 0, 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, ack, authority := reclamationProtocolFixture()
				left, right := net.Pipe()
				defer left.Close()
				defer right.Close()
				input, output := io.Pipe()
				defer input.Close()
				defer output.Close()
				frames := make(chan []byte, 1)
				done := make(chan struct{})
				go func() {
					defer close(done)
					frame, err := readReclamationFrame(bufio.NewReader(input))
					if err != nil {
						t.Error(err)
						return
					}
					if !tc.stale {
						authority.RequestID = frame.Challenge
					}
					time.Sleep(tc.reply)
					data, _ := json.Marshal(authority)
					frames <- data
				}()
				started := time.Now()
				_, deadline, err := requestReclamationAuthority(context.Background(), left, json.NewEncoder(delayedReclamationWriter{output, tc.write}), frames, ack)
				if tc.name == "fresh" {
					if err != nil || deadline != started.Add(time.Minute) || time.Until(deadline) != 35*time.Second {
						t.Fatal("IPC delay regained authorization time", deadline, err)
					}
				} else if err == nil {
					t.Fatal("expired or replayed authorization accepted")
				}
				<-done
			})
		})
	}
}

func TestReclamationClientCancelsHTTPWhenHelperDisconnects(t *testing.T) {
	capture, ack, authority := reclamationProtocolFixture()
	started := make(chan struct{})
	client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		if _, err := readBoundedLine(bufio.NewReader(connection), maxHelperRequestBytes); err != nil {
			return
		}
		_ = json.NewEncoder(connection).Encode(finalizationReclamationFrame{Version: 1, Kind: "authorize", Challenge: authority.RequestID, Acknowledgement: &ack})
		<-started
		_ = connection.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.ReclaimSkillFinalization(ctx, "disconnect", capture, func(ctx context.Context, _ string, _ skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error) {
		close(started)
		<-ctx.Done()
		return skillmanager.ReclamationAuthorization{}, ctx.Err()
	})
	if err == nil || ctx.Err() != nil {
		t.Fatal("Helper disconnect did not cancel authorization before caller timeout", err)
	}
}

func TestReclamationClientValidatesChallengeAndCompletion(t *testing.T) {
	for _, kind := range []string{"valid", "wrong_challenge", "changed_ack", "changed_completion", "second_challenge", "resume_challenge"} {
		t.Run(kind, func(t *testing.T) {
			capture, ack, authority := reclamationProtocolFixture()
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				defer connection.Close()
				reader := bufio.NewReader(connection)
				if _, err := readBoundedLine(reader, maxHelperRequestBytes); err != nil {
					return
				}
				if kind == "changed_ack" {
					ack.Capture.Binding.UserID = ack.Capture.Binding.NodeID
				}
				challenge := finalizationReclamationFrame{Version: 1, Kind: "authorize", Challenge: authority.RequestID, Acknowledgement: &ack}
				encoder := json.NewEncoder(connection)
				if err := encoder.Encode(challenge); err != nil {
					return
				}
				if _, err := readBoundedLine(reader, maxReclamationFrameBytes); err != nil {
					return
				}
				if kind == "second_challenge" {
					_ = encoder.Encode(challenge)
					return
				}
				record := capture
				if kind == "changed_completion" {
					record.TreeDigest = strings.Repeat("b", 64)
				}
				_ = encoder.Encode(finalizationReclamationFrame{Version: 1, Kind: "reclaimed", Record: &record})
			})
			called := 0
			authorize := func(_ context.Context, challenge string, original skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error) {
				called++
				if !skillmanager.SameFinalizationInput(original.Capture, capture) {
					return authority, errors.New("changed input")
				}
				authority.RequestID = challenge
				if kind == "wrong_challenge" {
					authority.RequestID = capture.Binding.UserID
				}
				return authority, nil
			}
			var result skillmanager.FinalizationRecord
			var err error
			if kind == "resume_challenge" {
				result, err = client.ResumeSkillReclamation(context.Background(), "resume", capture.Binding.NodeID, capture.Binding.SessionID)
			} else {
				result, err = client.ReclaimSkillFinalization(context.Background(), "reclaim", capture, authorize)
			}
			if kind == "valid" {
				if err != nil || result != capture || called != 1 {
					t.Fatal("valid exchange lost exact capture", err)
				}
			} else if err == nil {
				t.Fatal("invalid exchange succeeded")
			}
			if (kind == "changed_ack" || kind == "resume_challenge") && called != 0 {
				t.Fatal("unbound challenge reached HTTP")
			}
		})
	}
}
