package runtimehelper

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestMigrationRecoveryDisconnectCancelsLifecycleWait(t *testing.T) {
	for _, ending := range []string{"cancel", "deadline"} {
		t.Run(ending, func(t *testing.T) {
			file, err := os.CreateTemp("", "migration-recovery-socket-")
			if err != nil {
				t.Fatal(err)
			}
			path := file.Name()
			file.Close()
			os.Remove(path)
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			defer os.Remove(path)
			server := NewServer(path, -1, os.Getuid(), Engine{})
			server.mu.Lock()
			defer server.mu.Unlock()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				connection, err := listener.Accept()
				if err == nil {
					server.handle(context.Background(), connection)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			if ending == "cancel" {
				timer := time.AfterFunc(50*time.Millisecond, cancel)
				defer timer.Stop()
			}
			if _, err := NewClient(path).Call(ctx, "recovery", migrationRecoveryOperation, map[string]any{}); err == nil {
				t.Fatal("cancelled recovery succeeded")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("disconnected recovery retained a lifecycle waiter")
			}
		})
	}
}
