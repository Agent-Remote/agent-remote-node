package skillmanager

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRestoredServerSnapshotMaterializesOnFreshNode(t *testing.T) {
	fixture := os.Getenv("AGENT_REMOTE_RESTORE_FIXTURE")
	if fixture == "" {
		t.Skip("requires authorized download from the coordinated Server restore test")
	}
	if os.Geteuid() != 0 {
		t.Fatal("restore acceptance requires an isolated root container")
	}
	data, err := os.ReadFile(filepath.Join(fixture, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := os.OpenRoot(filepath.Join(fixture, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	opener := func(_ context.Context, digest string) (io.ReadCloser, error) { return objects.Open(digest) }
	store, storePath := privateStore(t)
	baseline, err := Materialize(context.Background(), store, "restored", manifest, opener, runtimeOptions())
	if err != nil {
		t.Fatal(err)
	}
	work, err := os.OpenRoot(filepath.Join(storePath, "restored/work"))
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	for _, entry := range baseline.Materialized.Entries {
		info, err := work.Lstat(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != 12345 || stat.Gid != 12345 || uint32(info.Mode().Perm()) != entry.Mode {
			t.Fatalf("restored ownership or mode mismatch: %s", entry.Path)
		}
		if entry.Kind != "file" {
			continue
		}
		content, err := work.ReadFile(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyContent(entry, content); err != nil {
			t.Fatal(err)
		}
		original, err := objects.Stat(entry.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		if stat.Nlink != 1 || os.SameFile(original, info) {
			t.Fatal("restored work is not an independent ordinary copy")
		}
	}
	captured, err := CaptureWorkTree(context.Background(), work, baseline, DefaultCaptureOptions())
	if err != nil || !equalManifests(captured, manifest) {
		t.Fatalf("restored source modes or tree changed: %v", err)
	}
	for name, expected := range map[string][]byte{
		"notes/state.bin": {0, 255, 's', 'a', 'v', 'e', 'd', '-', 'b', 'i', 'n', 'a', 'r', 'y', '-', 's', 't', 'a', 't', 'e', 0},
		"root-helper.txt": []byte("account-directory auxiliary state"),
		"learning/one":    []byte("left1"),
		"learning/two":    []byte("right2"),
	} {
		content, err := work.ReadFile(name)
		if err != nil || !bytes.Equal(content, expected) {
			t.Fatalf("restored semantic content differs: %s: %v", name, err)
		}
	}
	for _, name := range []string{"learning/removed.txt", "learning/recovered.txt"} {
		if _, err := work.Lstat(name); !os.IsNotExist(err) {
			t.Fatalf("deleted or detached state entered current work: %s", name)
		}
	}
}
