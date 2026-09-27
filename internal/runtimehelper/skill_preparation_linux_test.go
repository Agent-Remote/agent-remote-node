package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func transferredPreparationFixture(t *testing.T, content []byte) (Engine, SessionSpec, skillmanager.SkillSnapshot) {
	t.Helper()
	engine, spec, _ := nativeSkillFixture(t, false)
	// The fixture supplies only a trusted spec; the transport must prepare the new bundle itself.
	if err := os.RemoveAll(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)); err != nil {
		t.Fatal(err)
	}
	return engine, spec, preparationTestSnapshot(t, content)
}

func TestSkillPreparationLinuxHTTPToPrivateBundleAndExactReplay(t *testing.T) {
	content := []byte(strings.Repeat("learned instructions\n", 100_000))
	engine, spec, snapshot := transferredPreparationFixture(t, content)
	entry := snapshot.Manifest.Entries[0]
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer private-test-token" || request.URL.Query().Get("task_id") != snapshot.TaskID {
			t.Error("content transfer lost its exact task or authentication")
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		prefix := "/api/v1/node/skill-snapshots/" + snapshot.SnapshotID
		switch request.URL.Path {
		case prefix:
			_ = json.NewEncoder(writer).Encode(map[string]any{"schema_version": 1, "status": "ready", "committed": false, "data": snapshot, "errors": []any{}})
		case prefix + "/files/" + entry.SHA256:
			writer.Header().Set("Content-Type", "application/octet-stream")
			writer.Header().Set("ETag", `"`+entry.SHA256+`"`)
			writer.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
			_, _ = writer.Write(content)
		default:
			t.Error("unexpected authenticated content route")
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer httpServer.Close()
	apiClient := api.NewClient(httpServer.URL, "private-test-token")
	original, err := apiClient.GetSkillSnapshot(context.Background(), snapshot.SkillSnapshotIdentity)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	downloads := 0
	err = client.PrepareSkillSnapshot(context.Background(), "logical_preparation_task", original, func(ctx context.Context, entry skillmanager.Entry, writer io.Writer) error {
		downloads++
		return apiClient.ReadSkillSnapshotFile(ctx, original.SkillSnapshotIdentity, entry, writer)
	})
	if err != nil || downloads != 1 {
		t.Fatalf("HTTP-to-Helper preparation failed: %v, downloads=%d", err, downloads)
	}
	bundle, receipt, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	actual, err := bundle.ReadFile("work/notes")
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatal("private tree differs from authenticated content", err)
	}
	inputDigest, err := snapshot.InputDigest()
	if err != nil || receipt.Snapshot.Binding.TaskID != snapshot.TaskID || receipt.Snapshot.Binding.PreparationDigest != inputDigest || receipt.Runtime.UID != spec.RuntimeUID {
		t.Fatal("preparation did not retain original inputs and Helper identity", err)
	}
	file, err := bundle.OpenFile("work/notes", os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(file, "session learning")
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	neverDownload := func(context.Context, skillmanager.Entry, io.Writer) error {
		t.Error("replay or conflicting input redownloaded retained work")
		return errors.New("unexpected download")
	}
	if err := client.PrepareSkillSnapshot(context.Background(), "new_transport_request", snapshot, neverDownload); err != nil {
		t.Fatal("exact original preparation did not replay", err)
	}
	for name, change := range map[string]func(*skillmanager.SkillSnapshot){
		"task":       func(s *skillmanager.SkillSnapshot) { s.TaskID = s.AccountID },
		"epoch":      func(s *skillmanager.SkillSnapshot) { s.DirectoryEpoch++ },
		"generation": func(s *skillmanager.SkillSnapshot) { s.LibraryGeneration++ },
		"checkpoint": func(s *skillmanager.SkillSnapshot) { value := s.AccountID; s.StartingCheckpointID = &value },
		"system": func(s *skillmanager.SkillSnapshot) {
			s.SystemReleases = map[string]json.RawMessage{"ego-browser": json.RawMessage(`{"version":"changed"}`)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := snapshot
			change(&changed)
			if err := client.PrepareSkillSnapshot(context.Background(), "different_request", changed, neverDownload); err == nil {
				t.Fatal("changed fixed input reused the original bundle")
			}
		})
	}
	if data, err := bundle.ReadFile("work/notes"); err != nil || string(data) != "session learning" {
		t.Fatal("retry replaced learned content", err)
	}
}

func TestSkillPreparationLinuxFailureLeavesNoPartialBundle(t *testing.T) {
	for _, kind := range []string{"source_failure", "cancel", "bad_hash"} {
		t.Run(kind, func(t *testing.T) {
			engine, _, snapshot := transferredPreparationFixture(t, []byte("original"))
			server := NewServer("", -1, os.Geteuid(), engine)
			finished := make(chan struct{}, 1)
			client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
				server.handle(ctx, connection)
				finished <- struct{}{}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "bad_hash" {
				connection, err := net.Dial("unix", client.socketPath)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				data, _ := json.Marshal(snapshot)
				payload, _ := Map(skillPreparationHeader{SessionID: snapshot.SessionID, SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID, InputSize: int64(len(data))})
				_ = json.NewEncoder(connection).Encode(Request{Version: 1, RequestID: "bad_content", Operation: skillPreparationOperation, Payload: payload})
				reader := bufio.NewReader(connection)
				if frame, err := readSkillPreparationFrame(reader); err != nil || frame.Kind != "input" {
					t.Fatal("input handshake failed", err)
				}
				_, _ = connection.Write(data)
				if frame, err := readSkillPreparationFrame(reader); err != nil || frame.Kind != "object" {
					t.Fatal("object request failed", err)
				}
				_, _ = connection.Write([]byte("modified\x06"))
				if frame, err := readSkillPreparationFrame(reader); err == nil && frame.Kind == "prepared" {
					t.Fatal("Helper trusted client completion without hashing content")
				}
				_ = connection.Close()
			} else {
				err := client.PrepareSkillSnapshot(ctx, "failed_source", snapshot, func(_ context.Context, _ skillmanager.Entry, target io.Writer) error {
					_, err := io.WriteString(target, "original")
					if err != nil {
						return err
					}
					if kind == "cancel" {
						cancel()
						return ctx.Err()
					}
					return errors.New("source verification failed after bytes")
				})
				if err == nil {
					t.Fatal("failed transfer was accepted")
				}
			}
			<-finished
			entries, err := os.ReadDir(engine.config.SkillStateRoot)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed transfer left a published or staging bundle: %v, %v", entries, err)
			}
		})
	}
}

func TestSkillPreparationLinuxRejectsUnknownSystemAndRequiresTrustedSpec(t *testing.T) {
	engine, spec, snapshot := transferredPreparationFixture(t, []byte("original"))
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	// The stream remains bounded even when a large reference will be rejected before file reads.
	reference, _ := json.Marshal(strings.Repeat("a", 17<<20))
	snapshot.SystemReleases["large-reference"] = reference
	download := func(_ context.Context, _ skillmanager.Entry, writer io.Writer) error {
		_, err := io.WriteString(writer, "original")
		return err
	}
	if err := client.PrepareSkillSnapshot(context.Background(), "large_input", snapshot, download); err == nil {
		t.Fatal("unknown system release was accepted")
	}
	delete(snapshot.SystemReleases, "large-reference")
	if err := client.PrepareSkillSnapshot(context.Background(), "known_input", snapshot, download); err != nil {
		t.Fatal("known system preparation failed", err)
	}
	if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
		t.Fatal(err)
	}
	if err := client.PrepareSkillSnapshot(context.Background(), "missing_spec", snapshot, download); err == nil {
		t.Fatal("missing trusted spec authorized preparation replay")
	}
}

func TestSkillPreparationLinuxRejectsForeignSpecBindingsBeforeContent(t *testing.T) {
	engine, _, snapshot := transferredPreparationFixture(t, []byte("original"))
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	for name, change := range map[string]func(*skillmanager.SkillSnapshot){
		"node":     func(s *skillmanager.SkillSnapshot) { s.NodeID = s.TaskID },
		"user":     func(s *skillmanager.SkillSnapshot) { s.UserID = s.TaskID },
		"account":  func(s *skillmanager.SkillSnapshot) { s.AccountID = s.TaskID },
		"session":  func(s *skillmanager.SkillSnapshot) { s.SessionID = s.TaskID },
		"snapshot": func(s *skillmanager.SkillSnapshot) { s.SnapshotID = s.TaskID },
		"backend":  func(s *skillmanager.SkillSnapshot) { s.RuntimeBackend = "docker_sandbox" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := snapshot
			change(&changed)
			err := client.PrepareSkillSnapshot(context.Background(), "foreign_binding", changed, func(context.Context, skillmanager.Entry, io.Writer) error {
				t.Error("foreign preparation accessed content")
				return errors.New("unexpected download")
			})
			if err == nil {
				t.Fatal("foreign preparation reused a trusted session spec")
			}
		})
	}
	if entries, err := os.ReadDir(engine.config.SkillStateRoot); err != nil || len(entries) != 0 {
		t.Fatalf("foreign preparation changed private storage: %v, %v", entries, err)
	}
}
