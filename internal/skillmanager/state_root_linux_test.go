package skillmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateStoreRejectsUnsafeAncestorsAndAliases(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires the root helper identity")
	}
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "private", "skills")
	root, err := OpenStateStore(rootPath, filepath.Join(parent, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("marker", []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	info, err := os.Stat(rootPath)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("store mode: %v", err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(rootPath, alias); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenStateStore(alias); err == nil {
		_ = opened.Close()
		t.Fatal("linked skill root accepted")
	}
	if opened, err := OpenStateStore(rootPath, filepath.Join(alias, "not-created")); err == nil {
		_ = opened.Close()
		t.Fatal("shared path alias overlaps private state")
	}
	unsafeParent := filepath.Join(parent, "worker-owned")
	if err := os.Mkdir(unsafeParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(unsafeParent, 12345, 12345); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenStateStore(filepath.Join(unsafeParent, "skills")); err == nil {
		_ = opened.Close()
		t.Fatal("worker-owned ancestor accepted")
	}
	if _, err := os.Lstat(filepath.Join(unsafeParent, "skills")); !os.IsNotExist(err) {
		t.Fatal("created state below an unsafe ancestor")
	}
	if err := os.Chmod(filepath.Join(parent, "private"), 0o777); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenStateStore(rootPath); err == nil {
		_ = opened.Close()
		t.Fatal("writable non-sticky ancestor accepted")
	}
}
