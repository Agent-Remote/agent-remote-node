package skillexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const exportGrant = "fixture=." + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type exportFixture struct {
	permission skillmanager.NodeExportPermission
	record     skillmanager.FinalizationRecord
	manifest   skillmanager.Manifest
	files      map[string]string
	bytes      map[string][]byte
	checks     atomic.Int32
	denyAt     int32
	reads      int
	hold       *os.File
	holdError  error
}

func frozenFixture(t *testing.T) *exportFixture {
	t.Helper()
	f := &exportFixture{files: map[string]string{}, bytes: map[string][]byte{}}
	entries := []skillmanager.Entry{}
	for path, value := range map[string][]byte{"a": []byte("learned"), "b": {0, 1, 2}, "c": []byte("learned")} {
		sum := sha256.Sum256(value)
		digest := hex.EncodeToString(sum[:])
		kind := "text"
		if path == "b" {
			kind = "binary"
		}
		entries = append(entries, skillmanager.Entry{Path: path, Kind: "file", Mode: 0444, Size: int64(len(value)), SHA256: digest, ContentKind: kind})
		file := filepath.Join(t.TempDir(), digest)
		if err := os.WriteFile(file, value, 0600); err != nil {
			t.Fatal(err)
		}
		f.files[digest], f.bytes[digest] = file, value
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	f.manifest = skillmanager.Manifest{Version: 1, Entries: entries}
	digest, err := skillmanager.Digest(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	binding := skillmanager.NodeExportBinding{
		UserID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222",
		NodeID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444",
		SnapshotID: "55555555-5555-4555-8555-555555555555", TaskID: "66666666-6666-4666-8666-666666666666",
		LibraryGeneration: 9007199254740993, DirectoryEpoch: 9007199254740995, InitialTreeDigest: strings.Repeat("b", 64),
	}
	f.permission = skillmanager.NodeExportPermission{Binding: binding, DeviceID: "77777777-7777-4777-8777-777777777777", SSHKeyID: "88888888-8888-4888-8888-888888888888", ExpiresAt: time.Now().Add(10 * time.Minute).UTC(), RecheckSeconds: 1}
	f.record = skillmanager.FinalizationRecord{Version: 1, ObjectsVersion: 1, TreeDigest: digest, State: "upload_pending", Binding: skillmanager.SnapshotBinding{
		UserID: binding.UserID, AccountID: binding.AccountID, NodeID: binding.NodeID, SessionID: binding.SessionID,
		SnapshotID: binding.SnapshotID, TaskID: binding.TaskID, LibraryGeneration: binding.LibraryGeneration,
		DirectoryEpoch: binding.DirectoryEpoch, InitialTreeDigest: binding.InitialTreeDigest, PreparationDigest: strings.Repeat("c", 64),
	}}
	return f
}

func (f *exportFixture) identity() Identity {
	return Identity{f.permission.Binding.NodeID, f.permission.Binding.SnapshotID, f.permission.DeviceID, f.permission.SSHKeyID}
}

func (f *exportFixture) VerifyNodeExport(ctx context.Context, node, snapshot, device, key, grant string) (skillmanager.NodeExportPermission, error) {
	if f.checks.Add(1) == f.denyAt || grant != exportGrant || ctx.Err() != nil || (Identity{node, snapshot, device, key}) != f.identity() {
		return skillmanager.NodeExportPermission{}, ErrUnavailable
	}
	return f.permission, nil
}

func (f *exportFixture) InspectSkillFinalization(_ context.Context, _, node, session string) (skillmanager.FinalizationRecord, error) {
	f.reads++
	if node != f.record.Binding.NodeID || session != f.record.Binding.SessionID {
		return skillmanager.FinalizationRecord{}, ErrUnavailable
	}
	return f.record, nil
}

func (f *exportFixture) HoldSkillFinalization(_ context.Context, _ string, binding skillmanager.SnapshotBinding) (*os.File, skillmanager.FinalizationRecord, error) {
	if f.holdError != nil {
		return nil, skillmanager.FinalizationRecord{}, f.holdError
	}
	if binding != f.record.Binding {
		return nil, skillmanager.FinalizationRecord{}, ErrUnavailable
	}
	for _, path := range f.files {
		file, err := os.Open(path)
		f.hold = file
		return file, f.record, err
	}
	return nil, skillmanager.FinalizationRecord{}, ErrUnavailable
}

func (f *exportFixture) ReadSkillFinalization(_ context.Context, _ string, binding skillmanager.SnapshotBinding) (skillmanager.FinalizationRecord, skillmanager.Manifest, error) {
	f.reads++
	if f.hold == nil {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, errors.New("manifest read without lifetime hold")
	}
	if _, err := f.hold.Stat(); err != nil {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, err
	}
	if binding != f.record.Binding {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, ErrUnavailable
	}
	return f.record, f.manifest, nil
}

func (f *exportFixture) OpenSkillFinalizationObject(_ context.Context, _ string, record skillmanager.FinalizationRecord, digest string) (*os.File, skillmanager.Entry, error) {
	f.reads++
	if record != f.record {
		return nil, skillmanager.Entry{}, ErrUnavailable
	}
	for _, entry := range f.manifest.Entries {
		if entry.SHA256 == digest {
			file, err := os.Open(f.files[digest])
			return file, entry, err
		}
	}
	return nil, skillmanager.Entry{}, ErrUnavailable
}

type exportConnection struct {
	in     *bytes.Reader
	out    bytes.Buffer
	closed atomic.Bool
}

func connection() *exportConnection {
	data, _ := json.Marshal(map[string]any{"version": 1, "grant": exportGrant})
	return &exportConnection{in: bytes.NewReader(data)}
}
func (c *exportConnection) Read(b []byte) (int, error) { return c.in.Read(b) }
func (c *exportConnection) Write(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	return c.out.Write(b)
}
func (c *exportConnection) Close() error { c.closed.Store(true); return nil }

func frame(t *testing.T, reader io.Reader, value any) {
	t.Helper()
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil || size > maxManifestBytes {
		t.Fatal("invalid frame", err)
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}

func TestExportStreamsExactCompleteFrozenObjectsWithoutChangingRecord(t *testing.T) {
	f := frozenFixture(t)
	c := connection()
	original := f.record
	if err := Serve(context.Background(), c, f.identity(), f, f); err != nil {
		t.Fatal(err)
	}
	if output := os.Getenv("AGENT_REMOTE_TEST_EXPORT_STREAM_PATH"); output != "" {
		if err := os.WriteFile(output, c.out.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile("testdata/frozen-export-v1.bin")
	if err != nil || !bytes.Equal(golden, c.out.Bytes()) {
		t.Fatal("frozen export wire fixture changed", err)
	}
	reader := bytes.NewReader(c.out.Bytes())
	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != Magic {
		t.Fatal("invalid protocol")
	}
	var header Header
	frame(t, reader, &header)
	if header.Binding != f.permission.Binding || header.TreeDigest != f.record.TreeDigest || header.FileObjects != 2 {
		t.Fatal("identity or object deduplication changed")
	}
	objects, err := exportObjects(header.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range objects {
		data := make([]byte, entry.Size+1)
		if _, err := io.ReadFull(reader, data); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data[:len(data)-1], f.bytes[entry.SHA256]) || data[len(data)-1] != 1 {
			t.Fatal("object not verified completely")
		}
	}
	var complete completion
	frame(t, reader, &complete)
	if !complete.Complete || complete.TreeDigest != header.TreeDigest || complete.FileObjects != 2 || reader.Len() != 0 || f.record != original || f.checks.Load() < 3 || !c.closed.Load() {
		t.Fatal("export lost completion, reauthorization or immutable state")
	}
}

func TestExportRefusesUntrustedAndIncompleteInputs(t *testing.T) {
	for _, kind := range []string{"denied", "foreign_capture", "not_frozen", "manifest_changed", "missing_object", "corrupt_object", "extra_bytes", "expired", "wrong_termination"} {
		t.Run(kind, func(t *testing.T) {
			f, c := frozenFixture(t), connection()
			objects, _ := exportObjects(f.manifest)
			switch kind {
			case "denied":
				f.denyAt = 1
			case "foreign_capture":
				f.record.Binding.AccountID = f.record.Binding.UserID
			case "not_frozen":
				f.record.ObjectsVersion = 0
			case "manifest_changed":
				f.manifest.Entries[0].Mode = 0600
			case "missing_object":
				if err := os.Remove(f.files[objects[0].SHA256]); err != nil {
					t.Fatal(err)
				}
			case "corrupt_object":
				if err := os.WriteFile(f.files[objects[0].SHA256], bytes.Repeat([]byte{'x'}, int(objects[0].Size)), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra_bytes":
				if err := os.WriteFile(f.files[objects[0].SHA256], append(f.bytes[objects[0].SHA256], 42), 0600); err != nil {
					t.Fatal(err)
				}
			case "expired":
				f.permission.ExpiresAt = time.Now().Add(-time.Second)
			case "wrong_termination":
				digest, unclean := strings.Repeat("d", 64), false
				f.permission.IncomingDigest, f.permission.Unclean = &digest, &unclean
			}
			if err := Serve(context.Background(), c, f.identity(), f, f); !errors.Is(err, ErrUnavailable) {
				t.Fatal("invalid export succeeded", err)
			}
			if bytes.Contains(c.out.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("failed export claimed completion")
			}
			if kind == "denied" && (f.reads != 0 || c.out.Len() != 0) {
				t.Fatal("denial read local state or wrote content")
			}
		})
	}
}

func TestExportReauthorizationFailureNeverPublishesCompletion(t *testing.T) {
	for _, at := range []int32{2, 3} {
		f, c := frozenFixture(t), connection()
		f.denyAt = at
		if err := Serve(context.Background(), c, f.identity(), f, f); !errors.Is(err, ErrUnavailable) {
			t.Fatal("revoked export succeeded", at, err)
		}
		if bytes.Contains(c.out.Bytes(), []byte(`"complete":true`)) {
			t.Fatal("revoked export completed")
		}
		if at == 2 && c.out.Len() != 0 || at == 3 && c.out.Len() == 0 {
			t.Fatal("denial did not target the header/footer boundary", at, c.out.Len())
		}
	}
}

type blockedConnection struct {
	in   *bytes.Reader
	pipe *io.PipeWriter
}

func (c *blockedConnection) Read(b []byte) (int, error)  { return c.in.Read(b) }
func (c *blockedConnection) Write(b []byte) (int, error) { return c.pipe.Write(b) }
func (c *blockedConnection) Close() error                { return c.pipe.CloseWithError(io.ErrClosedPipe) }

func TestExportPeriodicRevocationClosesBlockedOutput(t *testing.T) {
	f := frozenFixture(t)
	f.denyAt = 3
	reader, writer := io.Pipe()
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	started := time.Now()
	err := Serve(ctx, &blockedConnection{connection().in, writer}, f.identity(), f, f)
	if !errors.Is(err, ErrUnavailable) || ctx.Err() != nil || time.Since(started) > 3*time.Second {
		t.Fatal("authorization loop did not release blocked stream", err)
	}
	if f.hold == nil {
		t.Fatal("blocked export acquired no read hold")
	}
	if _, err := f.hold.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("revoked export leaked its read hold", err)
	}
}

func TestExportHandshakeIsBoundedAndCanonical(t *testing.T) {
	for _, data := range []string{`{}`, `{"version":true,"grant":"x"}`, `{"version":1,"grant":"x","grant":"y"}`, `{"Version":1,"grant":"x"}`, `{"version":1,"grant":"x","path":"/etc/passwd"}`, strings.Repeat("x", 8193)} {
		c := &exportConnection{in: bytes.NewReader([]byte(data))}
		if _, err := readGrant(context.Background(), c); err == nil {
			t.Fatal("accepted invalid handshake")
		}
	}
}

func TestExportReadHoldIsRequiredAndReleased(t *testing.T) {
	for _, mode := range []string{"success", "hold_denied", "bad_manifest"} {
		t.Run(mode, func(t *testing.T) {
			f := frozenFixture(t)
			if mode == "hold_denied" {
				f.holdError = ErrUnavailable
			}
			if mode == "bad_manifest" {
				f.manifest.Entries[0].Size++
			}
			c := connection()
			err := Serve(context.Background(), c, f.identity(), f, f)
			if (err == nil) != (mode == "success") {
				t.Fatal("unexpected export hold result", err)
			}
			if mode == "hold_denied" {
				if f.reads != 1 || c.out.Len() != 0 {
					t.Fatal("denied hold read or exported content")
				}
			} else if f.hold == nil {
				t.Fatal("export acquired no lifetime hold")
			} else if _, err := f.hold.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("export leaked its read hold", err)
			}
		})
	}
}

type fixtureFrozenObjects struct {
	fixture *exportFixture
	capture skillmanager.FinalizationRecord
}

func (f *exportFixture) OpenSkillFinalizationObjects(_ context.Context, _ string, capture skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error) {
	return &fixtureFrozenObjects{f, capture}, nil
}

func (r *fixtureFrozenObjects) Open(ctx context.Context, digest string) (*os.File, skillmanager.Entry, error) {
	return r.fixture.OpenSkillFinalizationObject(ctx, "fixture-object", r.capture, digest)
}

func (r *fixtureFrozenObjects) Close() error { return nil }
