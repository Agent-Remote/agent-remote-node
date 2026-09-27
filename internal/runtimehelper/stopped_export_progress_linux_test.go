package runtimehelper

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
)

func TestHelperFinalizationStoppedExportBlockedOutputReleasesLifecycle(t *testing.T) {
	for _, trigger := range []string{"cancel", "trailing_input", "idle", "recovery_cancel", "recovery_trailing_input", "recovery_idle"} {
		t.Run(trigger, func(t *testing.T) {
			engine, _, binding := stoppedExportFixture(t)
			operation, magicExpected := stoppedExportOperation, skillexport.Magic
			if strings.HasPrefix(trigger, "recovery_") {
				operation, magicExpected = stoppedRecoveryOperation, skillexport.RecoveryMagic
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			payload, err := Map(binding)
			if err != nil {
				t.Fatal(err)
			}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			peer, connection := net.Pipe()
			defer peer.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				server.handleStoppedExport(parent, connection, bufio.NewReader(connection), Request{
					Version: ProtocolVersion, RequestID: "blocked-output", Operation: operation, Payload: payload,
				})
			}()
			_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
			magic := make([]byte, len(skillexport.Magic))
			if _, err := io.ReadFull(peer, magic); err != nil || string(magic) != magicExpected {
				t.Fatal("helper never reached authorized output", err)
			}
			started := time.Now()
			limit := 5 * time.Second
			switch strings.TrimPrefix(trigger, "recovery_") {
			case "cancel":
				cancel()
			case "trailing_input":
				_ = peer.SetWriteDeadline(time.Now().Add(limit))
				if _, err := peer.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
			case "idle":
				limit += skillexport.WriteTimeout
			}
			select {
			case <-done:
			case <-time.After(limit):
				t.Fatal("helper retained a blocked writer or disconnect pump")
			}
			if strings.HasSuffix(trigger, "idle") && time.Since(started) < skillexport.WriteTimeout-time.Second {
				t.Fatal("fixture exited before the independent output deadline")
			}
			if !server.mu.TryLock() {
				t.Fatal("failed output retained the lifecycle lock")
			}
			server.mu.Unlock()
			// Failure must leave the same stopped source available for a fresh complete read.
			client := skillPreparationTestPeer(t, server.handle)
			stream := client.StreamStoppedSkillExport
			if operation == stoppedRecoveryOperation {
				stream = client.StreamStoppedSkillRecovery
			}
			if err := stream(context.Background(), "retry", binding, io.Discard,
				func(skillexport.Header, bool) error { return nil }); err != nil {
				t.Fatal("failed output damaged retained source or blocked retry", err)
			}
		})
	}
}
