package runtimerecovery

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"golang.org/x/sys/unix"
)

func loseOriginalHelperReply(t *testing.T, ctx context.Context, task string) (*atomic.Bool, func()) {
	t.Helper()
	listener, err := net.Listen("unix", lossSocket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(lossSocket, 0, workerUID); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lossSocket, 0660); err != nil {
		t.Fatal(err)
	}
	var dropped atomic.Bool
	var handlers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer conn.Close()
				if !workerPeer(conn) {
					return
				}
				_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
				var request runtimehelper.Request
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				result, callErr := runtimehelper.NewClient(helperSocket).Call(ctx, request.RequestID, request.Operation, request.Payload)
				if request.Operation == "migrate_account" && request.RequestID == task && callErr == nil && result["migrated"] == true && dropped.CompareAndSwap(false, true) {
					return
				}
				response := runtimehelper.Response{Version: 1, OK: callErr == nil, Result: result}
				if callErr != nil {
					response.Error = &runtimehelper.Error{Code: "PROOF_FORWARD_FAILED", Message: "Disposable forwarding failed."}
				}
				_ = json.NewEncoder(conn).Encode(response)
			}()
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { _ = listener.Close(); <-done; handlers.Wait(); _ = os.Remove(lossSocket) }) }
	t.Cleanup(stop)
	return &dropped, stop
}

func workerPeer(connection net.Conn) bool {
	peer, ok := connection.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := peer.SyscallConn()
	if err != nil {
		return false
	}
	allowed := false
	if raw.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		allowed = err == nil && credential.Uid == workerUID
	}) != nil {
		return false
	}
	return allowed
}
