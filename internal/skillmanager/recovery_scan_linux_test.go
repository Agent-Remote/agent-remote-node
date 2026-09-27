package skillmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"golang.org/x/sys/unix"
)

func recoveryTreeFixture(t *testing.T) (*os.Root, string, PermissionBaseline) {
	t.Helper()
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	baseline, err := WritableBaseline(Manifest{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	return root, path, baseline
}

func TestRecoveryScanMatchesCaptureMetadataWithoutCreatingFiles(t *testing.T) {
	root, path, _ := recoveryTreeFixture(t)
	data := []byte("initial\n")
	baseline, err := WritableBaseline(Manifest{Version: 1, Entries: []Entry{objectEntry("original", data, 0o444)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty", "nested", "ego-browser"} {
		if err := root.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string][]byte{"original": data, "nested/binary": {0, 255}, "ego-browser/excluded": []byte("system")} {
		if err := os.WriteFile(filepath.Join(path, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.Chmod("original", 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link": "nested/binary", "nested/interpreter": "/managed/python3", "alias": "link"} {
		if err := root.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	options := DefaultCaptureOptions()
	options.SystemPaths = []string{"ego-browser"}
	options.RuntimeDependencies = map[string]string{"python3": "/managed/python3"}
	expected, err := captureWorkTree(context.Background(), root, baseline, options, false)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	first, err := WalkRecoveryTree(context.Background(), root, baseline, options, func(entry Entry) error {
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	if !reflect.DeepEqual(entries, expected.Entries) || first.Entries != int64(len(entries)) || first.FileBytes != int64(len(data)+2) {
		t.Fatal("recovery lost metadata, permissions, exclusions or complete byte count")
	}
	second, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil)
	if err != nil || first != second {
		t.Fatal("read-only repeated observation changed", err)
	}
}

func TestRecoveryScanRejectsUnsafeLinksAndSpecialEntries(t *testing.T) {
	for _, target := range []string{"/private/secret", "../secret", "absent", ".", "link", "ego-browser/private", "file/../file"} {
		t.Run(target, func(t *testing.T) {
			root, path, baseline := recoveryTreeFixture(t)
			if err := os.WriteFile(filepath.Join(path, "file"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := root.Symlink(target, "link"); err != nil {
				t.Fatal(err)
			}
			if _, err := WalkRecoveryTree(context.Background(), root, baseline, DefaultCaptureOptions(), nil); err == nil {
				t.Fatal("unsafe recovery link accepted")
			}
		})
	}
	t.Run("fifo", func(t *testing.T) {
		root, path, baseline := recoveryTreeFixture(t)
		if err := unix.Mkfifo(filepath.Join(path, "pipe"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := WalkRecoveryTree(context.Background(), root, baseline, DefaultCaptureOptions(), nil); err == nil {
			t.Fatal("special entry accepted")
		}
	})
}

func TestRecoveryScanRejectsMutationAndPropagatesCancellation(t *testing.T) {
	root, path, baseline := recoveryTreeFixture(t)
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := DefaultCaptureOptions()
	before, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WalkRecoveryTree(context.Background(), root, baseline, options, func(entry Entry) error {
		return os.WriteFile(filepath.Join(path, entry.Path), []byte("two"), 0o600)
	}); err == nil {
		t.Fatal("mutation inside visitor accepted")
	}
	after, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil)
	if err != nil || before.TreeDigest == after.TreeDigest || before.SourceDigest == after.SourceDigest {
		t.Fatal("complete observation failed to detect changed bytes and source", err)
	}
	if err := os.Remove(filepath.Join(path, "file")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil)
	if err != nil || after.TreeDigest != replaced.TreeDigest || after.SourceDigest == replaced.SourceDigest {
		t.Fatal("same-byte inode replacement was not distinguished", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = WalkRecoveryTree(ctx, root, baseline, options, func(Entry) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("visitor cancellation was ignored", err)
	}
	denied := errors.New("visitor denied")
	if _, err := WalkRecoveryTree(context.Background(), root, baseline, options, func(Entry) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("visitor denial was ignored", err)
	}
}

func TestRecoveryScanRequiresCompletePassComparisonForEarlierSiblingChanges(t *testing.T) {
	root, path, baseline := recoveryTreeFixture(t)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte("same"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	firstPath := ""
	options := DefaultCaptureOptions()
	// A callback can run after an earlier sibling was checked. Only comparison with another
	// full pass can certify that the provisional observation still describes the source.
	initial, err := WalkRecoveryTree(context.Background(), root, baseline, options, func(entry Entry) error {
		if firstPath == "" {
			firstPath = entry.Path
			return nil
		}
		return os.WriteFile(filepath.Join(path, firstPath), []byte("edit"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	final, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil)
	if err != nil || initial.TreeDigest == final.TreeDigest || initial.SourceDigest == final.SourceDigest {
		t.Fatal("final complete pass missed earlier sibling mutation", err)
	}
}

func TestRecoveryScanUsesIndependentByteBoundAndDepthLimit(t *testing.T) {
	root, path, baseline := recoveryTreeFixture(t)
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("above the configured quota"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := DefaultCaptureOptions()
	options.DirectoryBytes = 1
	if observed, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil); err != nil || observed.FileBytes <= 1 {
		t.Fatal("capture byte quota truncated recovery", err)
	}
	nested := ""
	for range 130 {
		nested += "d/"
		if err := root.Mkdir(nested, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil); err == nil {
		t.Fatal("unbounded recovery traversal depth accepted")
	}
}

func TestRecoveryScanExceedsManifestLimitInOneDirectory(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_RECOVERY_CAPACITY") != "1" {
		t.Skip("set AGENT_REMOTE_RUN_SKILL_RECOVERY_CAPACITY=1 for actual over-entry scanner acceptance")
	}
	root, path, baseline := recoveryTreeFixture(t)
	const count = 100_001
	for index := range count {
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("file-%06d", index)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	options := DefaultCaptureOptions()
	options.DirectoryBytes, options.Entries = 1, 1
	seen := int64(0)
	first, err := WalkRecoveryTree(context.Background(), root, baseline, options, func(entry Entry) error {
		if entry.Kind != "file" || entry.Size != 0 {
			t.Fatal("unexpected recovered entry")
		}
		seen++
		return nil
	})
	if err != nil || seen != count || first.Entries != count || first.FileBytes != 0 {
		t.Fatal("recovery truncated over-entry tree", seen, err)
	}
	second, err := WalkRecoveryTree(context.Background(), root, baseline, options, nil)
	if err != nil || second != first {
		t.Fatal("complete over-entry observation changed", err)
	}
	if _, err := CaptureWorkTree(context.Background(), root, baseline, DefaultCaptureOptions()); err == nil {
		t.Fatal("recovery silently raised the published v1 manifest limit")
	}
}
