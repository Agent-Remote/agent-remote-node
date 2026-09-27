package runtimehelper

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperFinalizationDescriptorRejectsMalformedFramesWithoutLeaking(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	manifestPath := filepath.Join(engine.config.SkillStateRoot, "session-"+record.Binding.SessionID, "finalization", "manifest.json")
	for _, mode := range []string{"single", "reader"} {
		t.Run(mode, func(t *testing.T) {
			for _, kind := range []string{"missing_fd", "two_fds", "truncated_rights", "incomplete", "oversized", "duplicate_key", "wrong_version", "wrong_binding", "wrong_size", "writable", "pipe", "trailing_json"} {
				t.Run(kind, func(t *testing.T) {
					file, err := os.Open(manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					info, _ := file.Stat()
					metadata := finalizationFileResponse{Record: record, Kind: "manifest", Size: info.Size()}
					if kind == "wrong_binding" {
						metadata.Record.Binding.DirectoryEpoch++
					}
					if kind == "wrong_size" {
						metadata.Size++
					}
					if kind == "writable" {
						_ = file.Close()
						file, err = os.OpenFile(manifestPath, os.O_RDWR, 0)
						if err != nil {
							t.Fatal(err)
						}
						defer file.Close()
					}
					if kind == "pipe" {
						_ = file.Close()
						read, write, err := os.Pipe()
						if err != nil {
							t.Fatal(err)
						}
						file = read
						defer read.Close()
						defer write.Close()
					}
					result, _ := Map(metadata)
					response := Response{Version: 1, OK: true, Result: result}
					if kind == "wrong_version" {
						response.Version++
					}
					encoded, _ := json.Marshal(response)
					data := string(encoded) + "\n"
					switch kind {
					case "incomplete":
						data = data[:len(data)/2]
					case "oversized":
						data = strings.Repeat("x", maxCaptureFileResponseBytes+1) + "\n"
					case "duplicate_key":
						data = strings.Replace(data, `"version":1`, `"version":1,"version":1`, 1)
					case "trailing_json":
						data += "{}"
					}
					descriptors := []int{int(file.Fd())}
					if kind == "missing_fd" {
						descriptors = nil
					}
					if kind == "two_fds" {
						descriptors = append(descriptors, int(file.Fd()))
					}
					if kind == "truncated_rights" {
						descriptors = make([]int, 32)
						for i := range descriptors {
							descriptors[i] = int(file.Fd())
						}
					}
					before := countCaptureDescriptors(t, manifestPath)
					client, stop := fakeCapturePeer(t, func(connection *net.UnixConn) {
						var rights []byte
						if len(descriptors) > 0 {
							rights = syscall.UnixRights(descriptors...)
						}
						_, _, _ = connection.WriteMsgUnix([]byte(data), rights, nil)
					})
					if mode == "reader" {
						var reader skillmanager.FrozenObjectReader
						reader, err = client.OpenSkillFinalizationObjects(context.Background(), "malformed-response", record)
						if reader != nil {
							_ = reader.Close()
						}
					} else {
						_, _, err = client.ReadSkillFinalization(context.Background(), "malformed-response", record.Binding)
					}
					stop()
					if err == nil {
						t.Fatal("malformed or unsafe transfer accepted")
					}
					if after := countCaptureDescriptors(t, manifestPath); after != before {
						t.Fatalf("descriptor leak: before %d, after %d", before, after)
					}
				})
			}
		})
	}
}

func TestHelperFinalizationCancellationReleasesBlockedHandler(t *testing.T) {
	for _, mode := range []string{"single", "reader"} {
		for _, ending := range []string{"cancel", "deadline"} {
			t.Run(mode+"/"+ending, func(t *testing.T) {
				engine, record := finalizationTransferFixture(t)
				file, err := os.CreateTemp("", "finalization-cancel-")
				if err != nil {
					t.Fatal(err)
				}
				path := file.Name()
				_ = file.Close()
				_ = os.Remove(path)
				listener, err := net.Listen("unix", path)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				defer os.Remove(path)
				server := NewServer(path, -1, os.Getuid(), engine)
				server.mu.Lock()
				defer server.mu.Unlock()
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					connection, err := listener.Accept()
					if err != nil {
						return
					}
					server.handle(context.Background(), connection)
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				if ending == "cancel" {
					timer := time.AfterFunc(50*time.Millisecond, cancel)
					defer timer.Stop()
				}
				defer cancel()
				client := NewClient(path)
				if mode == "reader" {
					reader, openErr := client.OpenSkillFinalizationObjects(ctx, "blocked-read", record)
					err = openErr
					if reader != nil {
						_ = reader.Close()
					}
				} else {
					_, _, err = client.ReadSkillFinalization(ctx, "blocked-read", record.Binding)
				}
				if err == nil {
					t.Fatal("blocked read ignored cancellation")
				}
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("disconnected reader retained a blocked Helper handler")
				}
			})
		}
	}
}

func TestHelperFinalizationRejectsIncompleteCanonicalMetadata(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	path := filepath.Join(engine.config.SkillStateRoot, "session-"+record.Binding.SessionID, "finalization/manifest.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	for _, mutation := range []string{"unclean_missing", "unclean_null", "binding_alias", "record_alias", "size_missing", "size_null", "entry_missing", "objects_missing", "objects_legacy", "generation_missing", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			metadata, _ := Map(finalizationFileResponse{Record: record, Kind: "manifest", Size: info.Size()})
			saved := metadata["record"].(map[string]any)
			binding := saved["binding"].(map[string]any)
			switch mutation {
			case "unclean_missing":
				delete(saved, "unclean")
			case "unclean_null":
				saved["unclean"] = nil
			case "binding_alias":
				binding["Node_ID"] = binding["node_id"]
				delete(binding, "node_id")
			case "record_alias":
				saved["State"] = saved["state"]
				delete(saved, "state")
			case "size_missing":
				delete(metadata, "size")
			case "size_null":
				metadata["size"] = nil
			case "entry_missing":
				delete(metadata, "entry")
			case "objects_missing":
				delete(saved, "objects_version")
			case "objects_legacy":
				saved["objects_version"] = 0
			case "generation_missing":
				delete(binding, "library_generation")
			case "extra":
				metadata["path"] = "/private"
			}
			data, _ := json.Marshal(Response{Version: 1, OK: true, Result: metadata})
			data = append(data, '\n')
			before := countCaptureDescriptors(t, path)
			client, closePeer := fakeCapturePeer(t, func(connection *net.UnixConn) {
				_, _, _ = connection.WriteMsgUnix(data, syscall.UnixRights(int(file.Fd())), nil)
			})
			_, _, err := client.ReadSkillFinalization(context.Background(), "malformed-metadata", record.Binding)
			closePeer()
			if err == nil || countCaptureDescriptors(t, path) != before {
				t.Fatal("incomplete metadata accepted or leaked a descriptor", err)
			}
		})
	}
}

func TestHelperFinalizationAcceptsFragmentedDescriptorMetadata(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	path := filepath.Join(engine.config.SkillStateRoot, "session-"+record.Binding.SessionID, "finalization/manifest.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	metadata, _ := Map(finalizationFileResponse{Record: record, Kind: "manifest", Size: info.Size()})
	data, _ := json.Marshal(Response{Version: 1, OK: true, Result: metadata})
	data = append(data, '\n')
	client, closePeer := fakeCapturePeer(t, func(connection *net.UnixConn) {
		_, _ = connection.Write(data[:17])
		_, _, _ = connection.WriteMsgUnix(data[17:40], syscall.UnixRights(int(file.Fd())), nil)
		_, _ = connection.Write(data[40:])
		var ack [1]byte
		if _, err := io.ReadFull(connection, ack[:]); err != nil || ack[0] != fileDescriptorTransferAck {
			t.Error("descriptor ACK missing", err)
		}
	})
	got, manifest, err := client.ReadSkillFinalization(context.Background(), "fragmented", record.Binding)
	closePeer()
	if err != nil || got != record || len(manifest.Entries) != 1 {
		t.Fatal("fragmented metadata failed", err)
	}
}
