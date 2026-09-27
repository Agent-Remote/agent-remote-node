package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

func managedSpecClientInput(t *testing.T) ManagedSessionSpecRequest {
	snapshot := preparationTestSnapshot(t, nil)
	return ManagedSessionSpecRequest{Snapshot: snapshot.SkillSnapshotIdentity, SnapshotInputDigest: strings.Repeat("a", 64), SystemReleases: testSkillSystemPins(), Session: toolsessions.CreatePayload{
		SessionID: snapshot.SessionID, UserID: snapshot.UserID, ToolAccountID: snapshot.AccountID, ToolType: "claude", RuntimeBackend: "native", WorkspaceID: snapshot.TaskID,
	}}
}

func TestManagedSpecCancellationClosesClientAndLockWait(t *testing.T) {
	server := NewServer("", -1, -1, NewEngine(EngineConfig{}))
	server.mu.Lock()
	defer server.mu.Unlock()
	entered, finished := make(chan struct{}), make(chan struct{})
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
		reader := bufio.NewReader(connection)
		data, err := readBoundedLine(reader, maxHelperRequestBytes)
		if err != nil {
			t.Error(err)
			close(finished)
			return
		}
		var request Request
		if err := json.Unmarshal(data, &request); err != nil {
			t.Error(err)
		}
		close(entered)
		server.handleManagedSpec(ctx, connection, reader, request)
		close(finished)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.PrepareManagedSessionSpec(ctx, "managed-task", managedSpecClientInput(t))
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not received")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("client ignored cancellation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnected handler retained lock wait")
	}
}

func TestManagedSpecClientRejectsWrongReceipt(t *testing.T) {
	for _, field := range []string{"status", "session_id", "task_record_id", "skill_snapshot_id", "runtime_backend", "runtime_uid", "extra"} {
		t.Run(field, func(t *testing.T) {
			input := managedSpecClientInput(t)
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				_, _ = bufio.NewReader(connection).ReadBytes('\n')
				result := map[string]any{"status": "spec_ready", "session_id": input.Snapshot.SessionID, "skill_snapshot_id": input.Snapshot.SnapshotID, "task_record_id": input.Snapshot.TaskID, "runtime_backend": "native", "runtime_uid": 12345}
				result[field] = "wrong"
				_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
			})
			if _, err := client.PrepareManagedSessionSpec(context.Background(), "managed-task", input); err == nil {
				t.Fatal("wrong receipt accepted")
			}
		})
	}
}

func TestManagedSpecInputDigestExcludesOnlyNonce(t *testing.T) {
	input := managedSpecClientInput(t)
	config := EngineConfig{}
	original, err := managedSpecInputDigest("original", input, config)
	if err != nil {
		t.Fatal(err)
	}
	input.Session.EgoBrowserBrokerNonce = "secret-test-nonce"
	rotated, _ := managedSpecInputDigest("original", input, config)
	if original != rotated {
		t.Fatal("nonce persisted in immutable digest")
	}
	input.Session.Argv = []string{"changed"}
	changed, _ := managedSpecInputDigest("original", input, config)
	if original == changed {
		t.Fatal("launch input not bound")
	}
}
