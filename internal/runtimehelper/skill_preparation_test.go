package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func preparationTestSnapshot(t *testing.T, content []byte) skillmanager.SkillSnapshot {
	t.Helper()
	hash := sha256.Sum256(content)
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{{Path: "notes", Kind: "file", Mode: 0o444, Size: int64(len(content)), SHA256: hex.EncodeToString(hash[:]), ContentKind: "text"}}}
	digest, err := skillmanager.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	encodedRelease, err := json.Marshal(testSkillSystemPins().EgoBrowser)
	if err != nil {
		t.Fatal(err)
	}
	return skillmanager.SkillSnapshot{
		SkillSnapshotIdentity: skillmanager.SkillSnapshotIdentity{
			UserID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222",
			NodeID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444",
			SnapshotID: "55555555-5555-4555-8555-555555555555", TaskID: "66666666-6666-4666-8666-666666666666", RuntimeBackend: "native",
		},
		LibraryGeneration: 9007199254740993, DirectoryEpoch: 9007199254740995,
		TreeDigest: digest, Manifest: manifest, Items: []skillmanager.SkillSnapshotItem{}, SystemReleases: map[string]json.RawMessage{"ego-browser": encodedRelease},
	}
}

func testSkillSystemPins() skillmanager.SystemReleasePins {
	return skillmanager.SystemReleasePins{EgoBrowser: skillmanager.EgoBrowserRelease{
		Version: egobrowserartifact.OfficialSkillVersion, Commit: egobrowserartifact.OfficialSkillSourceCommit, TreeSHA256: egobrowserartifact.OfficialSkillTreeSHA256,
	}}
}

func skillPreparationTestPeer(t *testing.T, handle func(context.Context, net.Conn)) Client {
	t.Helper()
	file, err := os.CreateTemp("", "skill-preparation-")
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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var handlers sync.WaitGroup
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer connection.Close()
				// Keep the fixture longer than the production managed operation's 30-second
				// context so the fixture cannot masquerade as a lifecycle cancellation.
				_ = connection.SetDeadline(time.Now().Add(45 * time.Second))
				handle(ctx, connection)
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		<-done
		handlers.Wait()
		_ = os.Remove(path)
	})
	return NewClient(path)
}

func readPreparationTestInput(t *testing.T, connection net.Conn) (*bufio.Reader, skillmanager.SkillSnapshot) {
	t.Helper()
	reader := bufio.NewReader(connection)
	line, err := readBoundedLine(reader, maxHelperRequestBytes)
	if err != nil {
		t.Error(err)
		return reader, skillmanager.SkillSnapshot{}
	}
	var request Request
	if err := json.Unmarshal(line, &request); err != nil {
		t.Error(err)
	}
	header, err := validateSkillPreparationRequest(request)
	if err != nil {
		t.Error(err)
		return reader, skillmanager.SkillSnapshot{}
	}
	_ = json.NewEncoder(connection).Encode(skillPreparationFrame{Version: 1, Kind: "input"})
	data := make([]byte, header.InputSize)
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Error(err)
	}
	snapshot, err := decodeSkillPreparationInput(data)
	if err != nil {
		t.Error(err)
	}
	return reader, snapshot
}

func TestSkillPreparationClientStreamsExactObjects(t *testing.T) {
	content := []byte(strings.Repeat("私有\x06数据\n", 100_000))
	snapshot := preparationTestSnapshot(t, content)
	client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		reader, got := readPreparationTestInput(t, connection)
		if got.LibraryGeneration != snapshot.LibraryGeneration || got.DirectoryEpoch != snapshot.DirectoryEpoch {
			t.Error("helper stream lost original integer precision")
		}
		encoder := json.NewEncoder(connection)
		_ = encoder.Encode(skillPreparationFrame{Version: 1, Kind: "object", Digest: got.Manifest.Entries[0].SHA256})
		actual := make([]byte, len(content)+1)
		if _, err := io.ReadFull(reader, actual); err != nil || !bytes.Equal(actual[:len(content)], content) || actual[len(content)] != skillObjectComplete {
			t.Error("helper did not receive exact object plus completion", err)
		}
		_ = encoder.Encode(skillPreparationFrame{Version: 1, Kind: "prepared", SnapshotID: got.SnapshotID, TaskID: got.TaskID, TreeDigest: got.TreeDigest})
	})
	err := client.PrepareSkillSnapshot(context.Background(), "prepare_1", snapshot, func(_ context.Context, entry skillmanager.Entry, target io.Writer) error {
		if entry != snapshot.Manifest.Entries[0] {
			t.Error("helper changed object metadata")
		}
		_, err := io.Copy(target, bytes.NewReader(content))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSkillPreparationClientRejectsUnboundObjectAndCompletion(t *testing.T) {
	snapshot := preparationTestSnapshot(t, []byte("original"))
	for _, frame := range []skillPreparationFrame{
		{Version: 1, Kind: "object", Digest: strings.Repeat("a", 64)},
		{Version: 1, Kind: "prepared", SnapshotID: snapshot.TaskID, TaskID: snapshot.TaskID, TreeDigest: snapshot.TreeDigest},
		{Version: 1, Kind: "prepared", SnapshotID: snapshot.SnapshotID, TaskID: snapshot.SnapshotID, TreeDigest: snapshot.TreeDigest},
		{Version: 2, Kind: "failed"},
	} {
		client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
			_, _ = readPreparationTestInput(t, connection)
			_ = json.NewEncoder(connection).Encode(frame)
		})
		err := client.PrepareSkillSnapshot(context.Background(), "prepare_1", snapshot, func(context.Context, skillmanager.Entry, io.Writer) error {
			t.Error("unbound helper request reached authenticated downloader")
			return nil
		})
		if err == nil {
			t.Fatal("unbound helper response accepted")
		}
	}
}

func TestSkillPreparationClientDoesNotAcknowledgeFailedDownload(t *testing.T) {
	snapshot := preparationTestSnapshot(t, []byte("original"))
	for _, kind := range []string{"source_failure", "short", "excess", "wrong_hash"} {
		t.Run(kind, func(t *testing.T) {
			received := make(chan []byte, 1)
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				reader, _ := readPreparationTestInput(t, connection)
				_ = json.NewEncoder(connection).Encode(skillPreparationFrame{Version: 1, Kind: "object", Digest: snapshot.Manifest.Entries[0].SHA256})
				data, _ := io.ReadAll(reader)
				received <- data
			})
			err := client.PrepareSkillSnapshot(context.Background(), "prepare_1", snapshot, func(_ context.Context, _ skillmanager.Entry, target io.Writer) error {
				data := "original"
				switch kind {
				case "short":
					data = "short"
				case "excess":
					data += "excess"
				case "wrong_hash":
					data = "modified"
				}
				_, err := io.WriteString(target, data)
				if kind == "source_failure" {
					return errors.New("download validation failed after complete bytes")
				}
				return err
			})
			if err == nil {
				t.Fatal("failed download was accepted")
			}
			if data := <-received; bytes.Contains(data, []byte{skillObjectComplete}) {
				t.Fatal("failed download sent a completion byte")
			}
		})
	}
}

func TestSkillPreparationDisconnectCancelsBetweenObjects(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	started, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		withSkillPreparationStream(context.Background(), server, server, func(ctx context.Context, _ io.Reader) {
			close(started)
			<-ctx.Done()
		})
	}()
	<-started
	_ = client.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnected preparation kept filesystem work alive")
	}
}

func TestSkillPreparationObjectRequiresCompletionAfterExactBytes(t *testing.T) {
	for _, content := range []string{"data", "data!", "dat", "data\x06"} {
		object := &skillPreparationObject{reader: strings.NewReader(content), remaining: 4}
		data, err := io.ReadAll(object)
		if content == "data\x06" {
			if err != nil || string(data) != "data" {
				t.Fatal("valid framed object failed", err)
			}
		} else if err == nil {
			t.Fatal("unverified or truncated object accepted")
		}
	}
}

func TestSkillPreparationClientCancellationClosesBlockedSocket(t *testing.T) {
	connected, disconnected := make(chan struct{}), make(chan struct{})
	client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		reader := bufio.NewReader(connection)
		if _, err := readBoundedLine(reader, maxHelperRequestBytes); err != nil {
			t.Error(err)
			return
		}
		close(connected)
		_, _ = io.Copy(io.Discard, reader)
		close(disconnected)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	snapshot := preparationTestSnapshot(t, []byte("original"))
	go func() {
		finished <- client.PrepareSkillSnapshot(ctx, "cancelled", snapshot, func(context.Context, skillmanager.Entry, io.Writer) error {
			return errors.New("unexpected download")
		})
	}()
	<-connected
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost its cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled client stayed blocked")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("cancelled client retained its socket")
	}
}

func TestSkillPreparationFramesRejectAliasedNullAndOversizedData(t *testing.T) {
	for _, data := range []string{
		`{"Version":1,"kind":"input"}`,
		`{"version":1,"kind":"input","Digest":""}`,
		`{"version":1,"kind":"input","digest":null}`,
		`{"version":1,"version":1,"kind":"input"}`,
		`{"version":1,"kind":"input"} {}`,
		strings.Repeat(" ", 4096) + `{}`,
	} {
		if _, err := readSkillPreparationFrame(bufio.NewReader(strings.NewReader(data + "\n"))); err == nil {
			t.Fatal("invalid preparation control frame accepted")
		}
	}
}

func TestSkillPreparationInputDigestPreservesAllFixedMetadata(t *testing.T) {
	snapshot := preparationTestSnapshot(t, []byte("original"))
	snapshot.SystemReleases["ego-browser"] = json.RawMessage(`{"version":"one","generation":9007199254740993}`)
	digest, err := snapshot.InputDigest()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.SystemReleases["ego-browser"] = json.RawMessage(`{"generation":9007199254740993,"version":"one"}`)
	if reordered, err := snapshot.InputDigest(); err != nil || reordered != digest {
		t.Fatal("object key order changed fixed input identity", err)
	}
	snapshot.SystemReleases["ego-browser"] = json.RawMessage(`{"generation":9007199254740992,"version":"one"}`)
	if changed, err := snapshot.InputDigest(); err != nil || changed == digest {
		t.Fatal("large integer precision was lost from fixed input identity", err)
	}
}

func TestSkillPreparationCancellationDoesNotWaitForLegacyMutation(t *testing.T) {
	server := NewServer("", -1, os.Geteuid(), NewEngine(EngineConfig{}))
	server.mu.Lock()
	defer server.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- server.lockSkillPreparation(ctx) }()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("queued preparation lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled preparation waited for the legacy mutation lock")
	}
}
