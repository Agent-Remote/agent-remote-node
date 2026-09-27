package skillmanager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparedWork(t *testing.T, source Manifest, objects map[string][]byte) (*os.Root, string, PermissionBaseline) {
	t.Helper()
	store, path := privateStore(t)
	baseline, err := Materialize(context.Background(), store, "copy", source, objectSource(objects), runtimeOptions())
	if err != nil {
		t.Fatal(err)
	}
	workPath := filepath.Join(path, "copy/work")
	work, err := os.OpenRoot(workPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = work.Close() })
	return work, workPath, baseline
}

func TestCapturePreservesSourceModesUntilRuntimeActuallyChangesThem(t *testing.T) {
	data := []byte("#!/bin/sh\nexit 0\n")
	entry := objectEntry("run", data, 0o555)
	source := Manifest{Version: 1, Entries: []Entry{entry}}
	work, path, baseline := preparedWork(t, source, map[string][]byte{entry.SHA256: data})
	final, err := CaptureWorkTree(context.Background(), work, baseline, DefaultCaptureOptions())
	if err != nil || !equalManifests(final, source) {
		t.Fatalf("preparation invented a runtime edit: %v", err)
	}
	if err := os.Chmod(filepath.Join(path, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	final, err = CaptureWorkTree(context.Background(), work, baseline, DefaultCaptureOptions())
	if err != nil || final.Entries[0].Mode != 0o700 {
		t.Fatalf("real permission change lost: %v", err)
	}
}

func TestCaptureKeepsInvalidSkillEditsRootDataAndDeletions(t *testing.T) {
	data := []byte("---\nname: sample\n---\nBody")
	entry := objectEntry("sample/SKILL.md", data, 0o444)
	source := Manifest{Version: 1, Entries: []Entry{
		{Path: "sample", Kind: "directory", Mode: 0o555}, entry,
		objectEntry("sample/removed", nil, 0o644),
	}}
	work, path, baseline := preparedWork(t, source, map[string][]byte{entry.SHA256: data, source.Entries[2].SHA256: {}})
	invalid := []byte("---\nthis is no longer valid frontmatter\x00\xff")
	if err := os.WriteFile(filepath.Join(path, "sample/SKILL.md"), invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, "sample/removed")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "root-state.db"), []byte{0, 1, 255}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	final, err := CaptureWorkTree(context.Background(), work, baseline, DefaultCaptureOptions())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Entry{}
	for _, value := range final.Entries {
		seen[value.Path] = value
	}
	if _, exists := seen["sample/removed"]; exists {
		t.Fatal("deleted file silently restored")
	}
	if seen["empty"].Kind != "directory" || seen["root-state.db"].ContentKind != "binary" {
		t.Fatal("root state or empty directory lost")
	}
	if err := VerifyContent(seen["sample/SKILL.md"], invalid); err != nil {
		t.Fatalf("invalid skill content was rejected or changed: %v", err)
	}
}

func TestCaptureUsesCompleteDirectoryLinksAndNeverReadsExternalTargets(t *testing.T) {
	work, path, baseline := preparedWork(t, Manifest{Version: 1}, nil)
	if err := os.MkdirAll(filepath.Join(path, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "b/state"), []byte("shared within snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../b/state", filepath.Join(path, "a/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/python3", filepath.Join(path, "python")); err != nil {
		t.Fatal(err)
	}
	options := DefaultCaptureOptions()
	options.RuntimeDependencies = map[string]string{"python3": "/usr/bin/python3"}
	final, err := CaptureWorkTree(context.Background(), work, baseline, options)
	if err != nil {
		t.Fatal(err)
	}
	if final.Entries[1].Kind != "symlink" || final.Entries[4].Kind != "runtime_link" {
		t.Fatal("links were dereferenced or omitted")
	}
	if err := os.Symlink("/etc/shadow", filepath.Join(path, "secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureWorkTree(context.Background(), work, baseline, options); err == nil || !strings.Contains(err.Error(), "portability_error") {
		t.Fatalf("external link captured: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(path, "secret")); err != nil {
		t.Fatal("failed capture removed the recoverable work tree")
	}
}

func TestCaptureQuotaCancellationAndSystemExclusionPreserveWork(t *testing.T) {
	work, path, baseline := preparedWork(t, Manifest{Version: 1}, nil)
	content := bytes.Repeat([]byte{0, 255}, 6<<20)
	if err := os.WriteFile(filepath.Join(path, "state.db"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "ego-browser"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/shadow", filepath.Join(path, "ego-browser/system-file")); err != nil {
		t.Fatal(err)
	}
	options := DefaultCaptureOptions()
	options.SystemPaths = []string{"ego-browser"}
	final, err := CaptureWorkTree(context.Background(), work, baseline, options)
	if err != nil || len(final.Entries) != 1 || final.Entries[0].Size != int64(len(content)) {
		t.Fatalf("large state was capped or system content captured: %v", err)
	}
	options.DirectoryBytes = 1024
	if _, err := CaptureWorkTree(context.Background(), work, baseline, options); err == nil || !strings.Contains(err.Error(), "quota_exceeded") {
		t.Fatalf("capture quota not enforced: %v", err)
	}
	info, err := os.Stat(filepath.Join(path, "state.db"))
	if err != nil || info.Size() != int64(len(content)) {
		t.Fatalf("quota failure truncated original state: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CaptureWorkTree(ctx, work, baseline, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestCaptureFinalVerificationRejectsSameLengthReplacements(t *testing.T) {
	root, path := privateStore(t)
	if err := os.WriteFile(filepath.Join(path, "state"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture := treeCapture{options: DefaultCaptureOptions(), observed: make(map[string]os.FileInfo), manifest: Manifest{Version: 1}}
	if err := capture.walk(context.Background(), root, "", 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "replacement"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(path, "replacement"), filepath.Join(path, "state")); err != nil {
		t.Fatal(err)
	}
	if err := capture.verify(context.Background(), root, ""); err == nil {
		t.Fatal("replaced runtime file accepted")
	}
}
