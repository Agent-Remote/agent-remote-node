package runtimehelper

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"time"

	"context"
	"golang.org/x/sys/unix"
	"io"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperFinalizationReaderSequentialDescriptorsAndCancellation(t *testing.T) {
	engine, capture := finalizationTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, 0)
	_, manifest, err := client.ReadSkillFinalization(context.Background(), "manifest", capture.Binding)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, err := client.OpenSkillFinalizationObjects(ctx, "reader", capture)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for range 3 {
		file, entry, err := reader.Open(ctx, manifest.Entries[0].SHA256)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("changed")); err == nil {
			t.Fatal("reader descriptor was writable")
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil || skillmanager.VerifyContent(entry, data) != nil {
			t.Fatal("reader descriptor changed content", err)
		}
	}
	cancel()
	if file, _, err := reader.Open(context.Background(), manifest.Entries[0].SHA256); err == nil || file != nil {
		t.Fatal("cancelled reader continued")
	}
}

func TestHelperFinalizationReaderRejectsForeignCaptureAndUnknownObject(t *testing.T) {
	engine, capture := finalizationTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, 0)
	changed := capture
	changed.Unclean = !capture.Unclean
	if reader, err := client.OpenSkillFinalizationObjects(context.Background(), "foreign", changed); err == nil || reader != nil {
		t.Fatal("foreign capture acquired reader")
	}
	reader, err := client.OpenSkillFinalizationObjects(context.Background(), "original", capture)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if file, _, err := reader.Open(context.Background(), strings.Repeat("0", 64)); err == nil || file != nil {
		t.Fatal("unknown object escaped reader scope")
	}
}

func TestHelperFinalizationReaderHoldSurvivesServerShutdown(t *testing.T) {
	engine, capture := finalizationTransferFixture(t)
	var reader skillmanager.FrozenObjectReader
	var object *os.File
	t.Run("original_server", func(t *testing.T) {
		client, _ := serveCaptureTest(t, engine, os.Getuid())
		_, manifest, err := client.ReadSkillFinalization(context.Background(), "manifest", capture.Binding)
		if err != nil {
			t.Fatal(err)
		}
		reader, err = client.OpenSkillFinalizationObjects(context.Background(), "reader", capture)
		if err != nil {
			t.Fatal(err)
		}
		object, _, err = reader.Open(context.Background(), manifest.Entries[0].SHA256)
		if err != nil {
			_ = reader.Close()
			t.Fatal(err)
		}
	})
	if reader == nil || object == nil {
		t.Fatal("reader did not open")
	}
	defer reader.Close()
	defer object.Close()
	manifestPath := filepath.Join(engine.config.SkillStateRoot, "session-"+capture.Binding.SessionID, "finalization/manifest.json")
	competitor, err := os.Open(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer competitor.Close()
	if err := unix.Flock(int(competitor.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("Helper shutdown released the client's manifest hold")
	}
	data, err := io.ReadAll(object)
	if err != nil || len(data) == 0 {
		t.Fatal("transferred object lost after shutdown", err)
	}
	_ = reader.Close()
	if err := unix.Flock(int(competitor.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal("reader closure leaked the manifest hold", err)
	}
}

func TestHelperFinalizationReaderCancelsBlockedObjectWithoutLeaking(t *testing.T) {
	engine, capture := finalizationTransferFixture(t)
	manifestPath := filepath.Join(engine.config.SkillStateRoot, "session-"+capture.Binding.SessionID, "finalization/manifest.json")
	for _, ending := range []string{"cancel", "deadline"} {
		t.Run(ending, func(t *testing.T) {
			file, err := os.Open(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			before := countCaptureDescriptors(t, manifestPath)
			requested := make(chan struct{})
			client, stop := fakeCapturePeer(t, func(connection *net.UnixConn) {
				input := bufio.NewReader(connection)
				if err := sendReaderFile(context.Background(), connection, input, file, capture, "manifest", nil); err != nil {
					t.Error(err)
					return
				}
				if _, err := readBoundedLine(input, maxHelperRequestBytes); err != nil {
					t.Error(err)
					return
				}
				close(requested)
				_, _ = io.Copy(io.Discard, input)
			})
			defer stop()
			reader, err := client.OpenSkillFinalizationObjects(context.Background(), "reader", capture)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				object, _, err := reader.Open(ctx, strings.Repeat("a", 64))
				if object != nil {
					_ = object.Close()
				}
				done <- err
			}()
			select {
			case <-requested:
			case <-time.After(2 * time.Second):
				t.Fatal("object request was not received")
			}
			if ending == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled object read succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled object read remained blocked")
			}
			_ = reader.Close()
			if after := countCaptureDescriptors(t, manifestPath); after != before {
				t.Fatalf("reader leaked a manifest descriptor: before %d, after %d", before, after)
			}
		})
	}
}
