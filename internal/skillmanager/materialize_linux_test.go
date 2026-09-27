package skillmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

func runtimeOptions() MaterializeOptions {
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid, gid = 12345, 12345
	}
	return MaterializeOptions{UID: uid, GID: gid, Policy: DefaultCopyPolicy()}
}

func privateStore(t *testing.T) (*os.Root, string) {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, path
}

func objectEntry(path string, content []byte, mode uint32) Entry {
	hash := sha256.Sum256(content)
	kind := "binary"
	classifier := textClassifier{}
	classifier.update(content)
	if classifier.isText() {
		kind = "text"
	}
	return Entry{Path: path, Kind: "file", Mode: mode, Size: int64(len(content)), SHA256: hex.EncodeToString(hash[:]), ContentKind: kind}
}

func objectSource(content map[string][]byte) ObjectOpener {
	return func(_ context.Context, digest string) (io.ReadCloser, error) {
		value, exists := content[digest]
		if !exists {
			return nil, errors.New("object not authorized")
		}
		return io.NopCloser(bytes.NewReader(value)), nil
	}
}

func TestMaterializedCopiesAreIndependentAndKeepProtectedBaseline(t *testing.T) {
	store, path := privateStore(t)
	content := []byte("#!/bin/sh\nprintf learning\n")
	entry := objectEntry("skill/scripts/run", content, 0o555)
	manifest := Manifest{Version: 1, Entries: []Entry{
		{Path: "skill", Kind: "directory", Mode: 0o555},
		{Path: "skill/alias", Kind: "symlink", Mode: 0o777, Target: "scripts/run"},
		{Path: "skill/scripts", Kind: "directory", Mode: 0o555}, entry,
	}}
	opener := objectSource(map[string][]byte{entry.SHA256: content})
	for _, name := range []string{"first", "second"} {
		baseline, err := Materialize(context.Background(), store, name, manifest, opener, runtimeOptions())
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(path, name, "work/skill/scripts/run"))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("copy not owner writable: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(path, name, "baseline.json"))
		if err != nil {
			t.Fatal(err)
		}
		var persisted PermissionBaseline
		if err := json.Unmarshal(data, &persisted); err != nil || !equalManifests(persisted.Source, baseline.Source) {
			t.Fatalf("invalid persisted baseline: %v", err)
		}
		info, err = os.Stat(filepath.Join(path, name, "baseline.json"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("baseline permissions: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "first/work/skill/alias"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(path, "second/work/skill/scripts/run"))
	if err != nil || !bytes.Equal(second, content) {
		t.Fatalf("session copies share mutable content: %v", err)
	}
	if _, err := Materialize(context.Background(), store, "first", manifest, opener, runtimeOptions()); err == nil {
		t.Fatal("existing work was overwritten")
	}
	first, _ := os.ReadFile(filepath.Join(path, "first/work/skill/scripts/run"))
	if string(first) != "changed" {
		t.Fatal("repeat preparation lost session edits")
	}
}

func TestIncompleteOrCorruptObjectsNeverPublishBundles(t *testing.T) {
	for _, supplied := range [][]byte{[]byte("wrong"), []byte("ok plus extra"), []byte("o")} {
		t.Run(string(supplied), func(t *testing.T) {
			store, path := privateStore(t)
			entry := objectEntry("data", []byte("ok"), 0o444)
			manifest := Manifest{Version: 1, Entries: []Entry{entry}}
			_, err := Materialize(context.Background(), store, "copy", manifest, objectSource(map[string][]byte{entry.SHA256: supplied}), runtimeOptions())
			if err == nil {
				t.Fatal("corrupt object accepted")
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 {
				t.Fatalf("partial copy survived failure: %v", err)
			}
		})
	}
}

func TestMaterializationPreflightDoesNotOpenContentOnRejectedInput(t *testing.T) {
	store, _ := privateStore(t)
	opener := func(context.Context, string) (io.ReadCloser, error) {
		t.Fatal("rejected input opened content")
		return nil, nil
	}
	entry := objectEntry("data", []byte("ok"), 0o600)
	manifest := Manifest{Version: 1, Entries: []Entry{entry}}
	for _, bundle := range []string{"../escape", "nested/directory", ".prepare-claimed"} {
		if _, err := Materialize(context.Background(), store, bundle, manifest, opener, runtimeOptions()); err == nil {
			t.Fatal("invalid bundle accepted")
		}
	}
	options := runtimeOptions()
	options.UID = 1 << 32
	if _, err := Materialize(context.Background(), store, "copy", manifest, opener, options); err == nil {
		t.Fatal("overflowed runtime UID could wrap to root")
	}
	options = runtimeOptions()
	options.Policy.DirectoryBytes = 1
	if _, err := Materialize(context.Background(), store, "copy", manifest, opener, options); err == nil || !strings.Contains(err.Error(), "quota_exceeded") {
		t.Fatalf("expanded byte quota not enforced: %v", err)
	}
	options = runtimeOptions()
	options.Policy.MinimumFreeBytes = math.MaxUint64
	if _, err := Materialize(context.Background(), store, "copy", manifest, opener, options); err == nil || !strings.Contains(err.Error(), "insufficient_storage") {
		t.Fatalf("disk reserve not enforced: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Materialize(ctx, store, "copy", manifest, opener, runtimeOptions()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not enforced: %v", err)
	}
}

func TestRuntimeDependencyLinksRequireAnExactHelperMapping(t *testing.T) {
	store, path := privateStore(t)
	manifest := Manifest{Version: 1, Entries: []Entry{{Path: "python", Kind: "runtime_link", Mode: 0o777, Target: "/usr/bin/python3", Dependency: "python3"}}}
	opener := objectSource(nil)
	if _, err := Materialize(context.Background(), store, "rejected", manifest, opener, runtimeOptions()); err == nil {
		t.Fatal("unverified external dependency accepted")
	}
	options := runtimeOptions()
	options.RuntimeDependencies = map[string]string{"python3": "/usr/bin/python3"}
	if _, err := Materialize(context.Background(), store, "accepted", manifest, opener, options); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(path, "accepted/work/python"))
	if err != nil || link != "/usr/bin/python3" {
		t.Fatalf("dependency was copied or changed: %v", err)
	}
}

func TestRuntimeFilesAreNotLimitedByInstallationFileQuota(t *testing.T) {
	store, path := privateStore(t)
	content := bytes.Repeat([]byte{0, 255}, 6<<20)
	entry := objectEntry("state.db", content, 0o600)
	manifest := Manifest{Version: 1, Entries: []Entry{entry}}
	if _, err := Materialize(context.Background(), store, "copy", manifest, objectSource(map[string][]byte{entry.SHA256: content}), runtimeOptions()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(path, "copy/work/state.db"))
	if err != nil || info.Size() != int64(len(content)) {
		t.Fatalf("runtime database was truncated: %v", err)
	}
}

func TestStreamClassificationMatchesWholeFileVerification(t *testing.T) {
	store, _ := privateStore(t)
	text := append(bytes.Repeat([]byte("x"), 65_535), []byte("你好")...)
	objects := map[string][]byte{}
	manifest := Manifest{Version: 1}
	for name, value := range map[string][]byte{"text": text, "truncated": text[:len(text)-1], "binary": {0, 255}, "empty": {}} {
		entry := objectEntry(name, value, 0o600)
		if err := VerifyContent(entry, value); err != nil {
			t.Fatal(err)
		}
		manifest.Entries = append(manifest.Entries, entry)
		objects[entry.SHA256] = value
	}
	sort.Slice(manifest.Entries, func(i, j int) bool { return manifest.Entries[i].Path < manifest.Entries[j].Path })
	if _, err := Materialize(context.Background(), store, "copy", manifest, objectSource(objects), runtimeOptions()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeUIDCanWriteButCannotChangeProtectedBaseline(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires the helper's root identity to run a separate runtime UID")
	}
	store, path := privateStore(t)
	content := []byte("#!/bin/sh\nprintf executed\n")
	entry := objectEntry("run", content, 0o555)
	manifest := Manifest{Version: 1, Entries: []Entry{entry}}
	if _, err := Materialize(context.Background(), store, "copy", manifest, objectSource(map[string][]byte{entry.SHA256: content}), runtimeOptions()); err != nil {
		t.Fatal(err)
	}
	work, err := os.Open(filepath.Join(path, "copy/work"))
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	command := exec.Command("/bin/sh", "-c", "cd /proc/self/fd/3 && ./run && printf learning >> run && mkdir new && printf saved > new/state && ! cat ../baseline.json")
	command.ExtraFiles = []*os.File{work}
	command.Dir = "/"
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 12345, Gid: 12345}}
	output, err := command.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte("executed")) {
		t.Fatalf("non-root runtime cannot use the writable copy: %v: %s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(path, "copy/work/new/state"))
	if err != nil || string(data) != "saved" {
		t.Fatalf("runtime-generated content missing: %v", err)
	}
}

type cancelingReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelingReader) Read(buffer []byte) (int, error) {
	read, err := r.Reader.Read(buffer)
	r.cancel()
	return read, err
}

func TestCanceledCopyAndWrongClassificationLeaveNoPartialTree(t *testing.T) {
	for _, cancelDuringRead := range []bool{false, true} {
		store, path := privateStore(t)
		data := bytes.Repeat([]byte("x"), 128*1024)
		entry := objectEntry("file", data, 0o600)
		ctx, cancel := context.WithCancel(context.Background())
		if !cancelDuringRead {
			entry.ContentKind = "binary"
		}
		opener := func(context.Context, string) (io.ReadCloser, error) {
			var reader io.Reader = bytes.NewReader(data)
			if cancelDuringRead {
				reader = cancelingReader{Reader: reader, cancel: cancel}
			}
			return io.NopCloser(reader), nil
		}
		_, err := Materialize(ctx, store, "copy", Manifest{Version: 1, Entries: []Entry{entry}}, opener, runtimeOptions())
		cancel()
		if err == nil {
			t.Fatal("canceled or misclassified copy published")
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatalf("partial copy remains: %v", err)
		}
	}
}

func TestReservedSystemEntriesAndPublicStoreAreRejected(t *testing.T) {
	store, path := privateStore(t)
	for _, name := range []string{"ego-browser", "agent-remote-device"} {
		manifest := Manifest{Version: 1, Entries: []Entry{{Path: name, Kind: "directory", Mode: 0o755}}}
		if _, err := Materialize(context.Background(), store, "copy", manifest, objectSource(nil), runtimeOptions()); err == nil {
			t.Fatal("system entry became an ordinary writable copy")
		}
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(context.Background(), store, "copy", Manifest{Version: 1}, objectSource(nil), runtimeOptions()); err == nil {
		t.Fatal("public store accepted")
	}
}
