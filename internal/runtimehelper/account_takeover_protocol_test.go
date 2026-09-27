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

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func takeoverProtocolBinding() skillmanager.AccountTakeoverBinding {
	digest, _ := skillmanager.AccountInventoryDigest(nil)
	return skillmanager.AccountTakeoverBinding{Version: 1, NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", AccountID: "33333333-3333-4333-8333-333333333333", TaskID: "44444444-4444-4444-8444-444444444444", TakeoverID: "55555555-5555-4555-8555-555555555555", RuntimeBackend: "native", DirectoryEpoch: 9007199254740993, InventoryDigest: digest}
}

func TestHelperTakeoverCancellationClosesClientAndQueuedHandler(t *testing.T) {
	server := NewServer("", -1, -1, NewEngine(EngineConfig{}))
	server.mu.Lock()
	defer server.mu.Unlock()
	entered, finished := make(chan struct{}), make(chan struct{})
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
		reader := bufio.NewReader(connection)
		data, err := readBoundedLine(reader, maxAccountTakeoverRequestBytes)
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
		server.handleAccountTakeover(ctx, connection, reader, request)
		close(finished)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.CaptureAccountTakeover(ctx, "capture", takeoverProtocolBinding(), nil)
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
		t.Fatal("handler retained lock wait")
	}
}

func TestHelperTakeoverCaptureRejectsIncompleteOrChangedReceipt(t *testing.T) {
	for _, change := range []string{"none", "binding", "receipt", "digest", "missing", "null", "alias", "extra"} {
		t.Run(change, func(t *testing.T) {
			binding := takeoverProtocolBinding()
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				_, _ = bufio.NewReader(connection).ReadBytes('\n')
				reply, err := Map(skillmanager.AccountCapture{Version: 1, Binding: binding, HelperReceiptID: binding.TakeoverID, TreeDigest: strings.Repeat("a", 64), SourceExists: false})
				if err != nil {
					t.Error(err)
					return
				}
				switch change {
				case "binding":
					reply["binding"].(map[string]any)["directory_epoch"] = 1
				case "receipt":
					reply["helper_receipt_id"] = "bad"
				case "digest":
					reply["tree_digest"] = "bad"
				case "missing":
					delete(reply, "source_exists")
				case "null":
					reply["source_exists"] = nil
				case "alias":
					reply["Source_Exists"] = false
					delete(reply, "source_exists")
				case "extra":
					reply["path"] = "/private"
				}
				_ = json.NewEncoder(connection).Encode(Response{Version: 1, OK: true, Result: reply})
			})
			receipt, err := client.CaptureAccountTakeover(context.Background(), "capture", binding, nil)
			if change == "none" {
				if err != nil || receipt.Binding != binding {
					t.Fatal(receipt, err)
				}
			} else if err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}
