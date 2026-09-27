package skillmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureFixture(t *testing.T) (*os.Root, *os.Root, string, AccountTakeoverBinding) {
	t.Helper()
	store, _ := privateStore(t)
	path := t.TempDir()
	source, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	fence := accountFenceFixture()
	if _, err := CloseAccountImports(store, fence); err != nil {
		t.Fatal(err)
	}
	binding := AccountTakeoverBinding{
		Version: 1, NodeID: fence.NodeID, UserID: fence.UserID, AccountID: fence.AccountID,
		TakeoverID: "44444444-4444-4444-8444-444444444444", TaskID: "55555555-5555-4555-8555-555555555555",
		RuntimeBackend: "native", DirectoryEpoch: 1, InventoryDigest: strings.Repeat("a", 64),
	}
	return store, source, path, binding
}

func capturePolicy() CopyPolicy {
	return CopyPolicy{DirectoryBytes: 1 << 20, Entries: 100}
}

func TestAccountCaptureRetainsOrdinaryPrivateCopiesAndOriginalModes(t *testing.T) {
	store, source, path, binding := captureFixture(t)
	for _, directory := range []string{"manual", "empty", "ego-browser"} {
		if err := os.Mkdir(filepath.Join(path, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string][]byte{
		"manual/SKILL.md": []byte("---\ninvalid yaml [\n"), "root.db": {0, 255, 11},
		"ego-browser/SKILL.md": []byte("excluded-system"),
	} {
		if err := os.WriteFile(filepath.Join(path, name), content, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../root.db", filepath.Join(path, "manual", "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(path, "root.db"), filepath.Join(path, "other.db")); err != nil {
		t.Fatal(err)
	}
	receipt, err := CaptureAccountSource(context.Background(), store, source, binding, capturePolicy())
	if err != nil || !receipt.SourceExists || !validSnapshotID(receipt.HelperReceiptID) {
		t.Fatalf("capture failed: %#v %v", receipt, err)
	}
	bundle, saved, manifest, err := OpenAccountCapture(store, binding)
	if err != nil || saved != receipt {
		t.Fatalf("reopen failed: %#v %v", saved, err)
	}
	defer bundle.Close()
	if len(manifest.Entries) != 6 {
		t.Fatalf("capture lost entries or retained system: %#v", manifest)
	}
	for _, entry := range manifest.Entries {
		if entry.Kind != "file" {
			continue
		}
		original, err := os.Stat(filepath.Join(path, entry.Path))
		if err != nil || entry.Mode != 0o444 || original.Mode().Perm() != 0o444 {
			t.Fatalf("original permissions changed: %#v %v", entry, err)
		}
		file, authorized, err := OpenAccountCaptureObject(store, binding, entry.SHA256)
		if err != nil || authorized.SHA256 != entry.SHA256 {
			t.Fatalf("object not authorized: %v", err)
		}
		info, _ := file.Stat()
		if os.SameFile(original, info) || info.Mode().Perm() != 0o600 {
			t.Fatal("capture aliases mutable source or exposes original modes")
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil || int64(len(data)) != entry.Size {
			t.Fatal("capture bytes lost", err)
		}
	}
	if _, _, err := OpenAccountCaptureObject(store, binding, strings.Repeat("f", 64)); err == nil {
		t.Fatal("digest alone authorized unrelated content")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if repeated, err := CaptureAccountSource(context.Background(), store, nil, binding, capturePolicy()); err != nil || repeated != receipt {
		t.Fatalf("retry depended on deleted original: %#v %v", repeated, err)
	}
}

func TestAccountCaptureMissingSourceIsDurableEmptyInput(t *testing.T) {
	store, source, path, binding := captureFixture(t)
	receipt, err := CaptureAccountSource(context.Background(), store, nil, binding, capturePolicy())
	if err != nil || receipt.SourceExists {
		t.Fatalf("missing source capture: %#v %v", receipt, err)
	}
	if err := os.WriteFile(filepath.Join(path, "late"), []byte("late"), 0o600); err != nil {
		t.Fatal(err)
	}
	repeated, err := CaptureAccountSource(context.Background(), store, source, binding, capturePolicy())
	if err != nil || repeated != receipt {
		t.Fatalf("empty capture was replaced: %#v %v", repeated, err)
	}
	bundle, _, manifest, err := OpenAccountCapture(store, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if len(manifest.Entries) != 0 {
		t.Fatal("late source entered the immutable capture")
	}
}

func TestAccountCaptureRejectsChangedBindingAndDamagedPublishedBundle(t *testing.T) {
	for _, damage := range []string{"owner", "task", "inventory", "epoch", "missing_record", "record_link", "manifest_link", "manifest", "object_link", "objects_link", "object_hardlink", "object_mode"} {
		t.Run(damage, func(t *testing.T) {
			store, source, path, binding := captureFixture(t)
			if err := os.WriteFile(filepath.Join(path, "file"), []byte("saved"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := CaptureAccountSource(context.Background(), store, source, binding, capturePolicy()); err != nil {
				t.Fatal(err)
			}
			bundle, _, manifest, err := OpenAccountCapture(store, binding)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			digest := manifest.Entries[0].SHA256
			objectPath := filepath.Join("objects", digest)
			switch damage {
			case "owner":
				binding.UserID = binding.TaskID
			case "task":
				binding.TaskID = binding.TakeoverID
			case "inventory":
				binding.InventoryDigest = strings.Repeat("f", 64)
			case "epoch":
				binding.DirectoryEpoch++
			case "missing_record":
				err = bundle.Remove("record.json")
			case "record_link", "manifest_link":
				name := strings.TrimSuffix(damage, "_link") + ".json"
				if err = bundle.Remove(name); err == nil {
					err = bundle.Symlink("missing", name)
				}
			case "manifest":
				file, openErr := bundle.OpenFile("manifest.json", os.O_WRONLY|os.O_TRUNC, 0)
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, err = file.WriteString(`{"version":1,"entries":[]}`)
				_ = file.Close()
			case "object_link":
				if err = bundle.Remove(objectPath); err == nil {
					err = bundle.Symlink("missing", objectPath)
				}
			case "objects_link":
				if err = bundle.Rename("objects", "saved"); err == nil {
					err = bundle.Symlink("saved", "objects")
				}
			case "object_hardlink":
				err = bundle.Link(objectPath, "alias")
			case "object_mode":
				err = bundle.Chmod(objectPath, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(damage, "object") {
				if file, _, err := OpenAccountCaptureObject(store, binding, digest); err == nil {
					_ = file.Close()
					t.Fatal("unsafe capture bytes were opened")
				}
				return
			}
			if _, err := CaptureAccountSource(context.Background(), store, source, binding, capturePolicy()); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("damaged capture was replayed or classified absent: %v", err)
			}
		})
	}
}

func TestAccountCaptureFailurePreservesSourceAndHasNoPublishedBundle(t *testing.T) {
	for _, failure := range []string{"quota", "cancel", "external_link", "fence", "special"} {
		t.Run(failure, func(t *testing.T) {
			store, source, path, binding := captureFixture(t)
			if err := os.WriteFile(filepath.Join(path, "source"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			policy := capturePolicy()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "quota":
				policy.DirectoryBytes = 1
			case "cancel":
				cancel()
			case "external_link":
				if err := os.Symlink("/etc/passwd", filepath.Join(path, "escape")); err != nil {
					t.Fatal(err)
				}
			case "fence":
				binding.DirectoryEpoch++
			case "special":
				if err := source.Mkdir("not-file", 0o700); err != nil {
					t.Fatal(err)
				}
				if _, err := openRetainedSourceFile(source, "not-file"); err == nil {
					t.Fatal("directory opened as file")
				}
				return
			}
			if _, err := CaptureAccountSource(ctx, store, source, binding, policy); err == nil {
				t.Fatal("unsafe capture succeeded")
			}
			if _, err := store.Lstat(accountCaptureName(binding.AccountID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("partial capture published", err)
			}
			data, err := os.ReadFile(filepath.Join(path, "source"))
			if err != nil || string(data) != "original" {
				t.Fatal("failure changed original", err)
			}
		})
	}
}

func TestAccountCaptureSourceOpenerRejectsAncestorAndLeafAliases(t *testing.T) {
	_, source, path, _ := captureFixture(t)
	if err := os.Mkdir(filepath.Join(path, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "real", "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"alias": "real", "leaf": "real/file", "dangling": "missing"} {
		if err := os.Symlink(target, filepath.Join(path, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"alias/file", "leaf", "dangling", "../outside"} {
		if file, err := openRetainedSourceFile(source, name); err == nil {
			_ = file.Close()
			t.Fatal("alias opened as captured object", name)
		}
	}
}
