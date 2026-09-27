package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestFrozenInspectionPreservesOriginalLargeGenerations(t *testing.T) {
	record, _, _ := reclamationProtocolFixture()
	page := skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{
		{SessionID: record.Binding.SessionID, Record: &record},
	}}
	client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		_, _ = bufio.NewReader(connection).ReadBytes('\n')
		result, err := Map(page)
		if err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(connection).Encode(Response{Version: 1, OK: true, Result: result})
	})
	got, err := client.InspectSkillFinalization(context.Background(), "inspect", record.Binding.NodeID, record.Binding.SessionID)
	if err != nil || got != record {
		t.Fatal("inspection changed the original integer identity", err)
	}
}

func TestFrozenInspectionCancellationClosesQueuedHandler(t *testing.T) {
	server := NewServer("", -1, -1, NewEngine(EngineConfig{}))
	server.mu.Lock()
	defer server.mu.Unlock()
	entered, finished := make(chan struct{}), make(chan struct{})
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
		reader := bufio.NewReader(connection)
		data, err := readBoundedLine(reader, maxHelperResponseBytes)
		if err != nil {
			t.Error(err)
			close(entered)
			close(finished)
			return
		}
		var request Request
		if err := json.Unmarshal(data, &request); err != nil {
			t.Error(err)
		}
		close(entered)
		server.handleFinalizationList(ctx, connection, reader, request)
		close(finished)
	})
	client.timeout = 2 * time.Second
	record, _, _ := reclamationProtocolFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.InspectSkillFinalization(ctx, "inspect", record.Binding.NodeID, record.Binding.SessionID)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("inspection request not received")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled inspection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("inspection ignored cancellation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled inspection retained its Helper lock waiter")
	}
}
