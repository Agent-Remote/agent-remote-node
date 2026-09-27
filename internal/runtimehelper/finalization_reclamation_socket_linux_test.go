package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperFinalizationReclamationSocketFreshHTTPAndReplay(t *testing.T) {
	engine, spec, capture := helperReclamationFixture(t, false, "started")
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	ack, err := skillmanager.ReadFinalizationAcknowledgement(bundle, capture.Binding)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/node/skill-finalizations/"+ack.Receipt.ID+"/reclamation-authorization" ||
			r.Header.Get("Authorization") != "Bearer reclamation-test" || len(r.URL.Query()) != 1 || !validSkillUUID(r.URL.Query().Get("request_id")) {
			t.Error("HTTP lost original identity or authenticated challenge", r.URL.Path)
		}
		grant, _, _ := helperReclamationGrant(r.Context(), ack)
		grant.RequestID = r.URL.Query().Get("request_id")
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "reclaimable", "committed": true, "retryable": false, "errors": []any{}, "data": grant})
	}))
	defer remote.Close()
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	transport := api.NewClient(remote.URL, "reclamation-test")
	if result, err := client.ReclaimSkillFinalization(context.Background(), "fresh", capture, transport.AuthorizeSkillReclamationChallenge); err != nil || result != capture {
		t.Fatal("fresh HTTP/socket reclamation failed", err)
	}
	if calls.Load() != 1 {
		t.Fatal("fresh authorization was not requested exactly once")
	}
	if _, complete, err := skillmanager.ReadFinalizationReclamation(bundle, capture.Binding); err != nil || !complete {
		t.Fatal("socket returned before durable reclamation", err)
	}
	replacement := NewServer("", -1, os.Geteuid(), engine)
	resumed := skillPreparationTestPeer(t, replacement.handle)
	if record, err := resumed.ResumeSkillReclamation(context.Background(), "resume", capture.Binding.NodeID, capture.Binding.SessionID); err != nil || record != capture {
		t.Fatal("new Helper could not replay completion", err)
	}
	if _, err := resumed.ReclaimSkillFinalization(context.Background(), "replay", capture, transport.AuthorizeSkillReclamationChallenge); err != nil || calls.Load() != 1 {
		t.Fatal("completed replay renewed authorization", err)
	}
}

func TestHelperFinalizationReclamationSocketRefusesUnmarkedResumeAndUntrustedFrames(t *testing.T) {
	for _, kind := range []string{"unmarked_resume", "foreign_node", "changed_capture", "stale_response", "extra_field", "truncated", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, capture := helperReclamationFixture(t, false, "started")
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			switch kind {
			case "unmarked_resume", "foreign_node":
				nodeID := capture.Binding.NodeID
				if kind == "foreign_node" {
					nodeID = capture.Binding.UserID
				}
				if _, err := client.ResumeSkillReclamation(ctx, "resume", nodeID, spec.SessionID); err == nil {
					t.Fatal("resume created intent or accepted foreign Node")
				}
			case "changed_capture":
				changed := capture
				changed.TreeDigest = strings.Repeat("b", 64)
				if _, err := client.ReclaimSkillFinalization(ctx, "changed", changed, func(context.Context, string, skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error) {
					t.Error("changed capture requested authority")
					return skillmanager.ReclamationAuthorization{}, errors.New("unexpected authorization")
				}); err == nil {
					t.Fatal("changed capture reclaimed")
				}
			default:
				connection, err := net.Dial("unix", client.socketPath)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				payload, _ := Map(finalizationCleanupRequest{Capture: capture})
				encoder := json.NewEncoder(connection)
				if err := encoder.Encode(Request{Version: 1, RequestID: "raw", Operation: finalizationReclamationOperation, Payload: payload}); err != nil {
					t.Fatal(err)
				}
				reader := bufio.NewReader(connection)
				frame, err := readReclamationFrame(reader)
				if err != nil || frame.Kind != "authorize" || frame.Acknowledgement == nil {
					t.Fatal("missing challenge", err)
				}
				grant, _, _ := helperReclamationGrant(ctx, *frame.Acknowledgement)
				grant.RequestID = frame.Challenge
				if kind == "stale_response" {
					grant.RequestID = capture.Binding.NodeID
				}
				data, _ := json.Marshal(grant)
				switch kind {
				case "extra_field":
					data = append([]byte(`{"deadline":"2040-01-01T00:00:00Z",`), data[1:]...)
				case "truncated":
					data = data[:len(data)/2]
				case "oversized":
					data = []byte(strings.Repeat("x", maxReclamationFrameBytes+1))
				}
				_, _ = connection.Write(append(data, '\n'))
				result, err := readReclamationFrame(reader)
				if err == nil && result.Kind != "pending" {
					t.Fatal("untrusted authority permitted reclamation")
				}
			}
			bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "original" {
				t.Fatal("rejected socket operation lost input", err)
			}
			if _, err := bundle.Lstat("finalization/reclamation.json"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("untrusted socket operation created intent", err)
			}
		})
	}
}

func TestHelperFinalizationReclamationSocketDisconnectReleasesLifecycleLock(t *testing.T) {
	engine, _, capture := helperReclamationFixture(t, false, "started")
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	ctx, cancel := context.WithCancel(context.Background())
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := client.ReclaimSkillFinalization(ctx, "cancel", capture, func(ctx context.Context, _ string, _ skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error) {
			close(started)
			<-ctx.Done()
			return skillmanager.ReclamationAuthorization{}, ctx.Err()
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("reclamation did not request authorization")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled socket operation succeeded")
	}
	wait, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := server.lockSkillPreparation(wait); err != nil {
		t.Fatal("disconnect retained lifecycle lock", err)
	}
	server.mu.Unlock()
}

func TestHelperFinalizationReclamationSocketRecoversUnreadCompletion(t *testing.T) {
	engine, spec, capture := helperReclamationFixture(t, false, "started")
	t.Run("lost_reply", func(t *testing.T) {
		client, _ := serveCaptureTest(t, engine, os.Geteuid())
		connection, err := net.Dial("unix", client.socketPath)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		payload, _ := Map(finalizationCleanupRequest{Capture: capture})
		encoder := json.NewEncoder(connection)
		if err := encoder.Encode(Request{Version: 1, RequestID: "unread", Operation: finalizationReclamationOperation, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		frame, err := readReclamationFrame(bufio.NewReader(connection))
		if err != nil || frame.Acknowledgement == nil {
			t.Fatal(err)
		}
		grant, _, _ := helperReclamationGrant(context.Background(), *frame.Acknowledgement)
		grant.RequestID = frame.Challenge
		if err := encoder.Encode(grant); err != nil {
			t.Fatal(err)
		}
		bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		defer bundle.Close()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for {
			if _, complete, err := skillmanager.ReadFinalizationReclamation(bundle, capture.Binding); err == nil && complete {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("reclamation did not complete before unread connection was closed")
			}
		}
		// No completion frame is consumed. Shutdown also joins the original Helper handlers.
	})
	client, _ := serveCaptureTest(t, engine, os.Geteuid())
	if record, err := client.ResumeSkillReclamation(context.Background(), "recover-unread", capture.Binding.NodeID, spec.SessionID); err != nil || record != capture {
		t.Fatal("replacement Helper lost completed original reclamation", err)
	}
}
