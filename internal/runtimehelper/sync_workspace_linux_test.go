//go:build linux

package runtimehelper

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/workspace"
	"golang.org/x/sys/unix"
)

func TestSyncWorkspaceDescriptorNormalization(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	payload := workspace.PreparePayload{UserID: "user", WorkspaceID: "workspace", SyncSessionID: "sync"}
	directory, err := workspace.OpenDirectory(root, payload)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, err := directory.WriteMarker(payload); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory.Path, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory.Path, "content"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "user"), filepath.Join(root, "retained")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "user")); err != nil {
		t.Fatal(err)
	}
	identity := runtimeIdentity{UID: os.Getuid(), GID: os.Getgid()}
	remaining := 1000
	if err := normalizeSyncTree(context.Background(), directory.File, identity, uint32(os.Getuid()), 0, &remaining); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("outside target permissions changed: %v %v", info, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("outside target changed: %q %v", data, err)
	}
	content := filepath.Join(root, "retained", "workspaces", "workspace", "files", "content")
	info, err = os.Stat(content)
	if err != nil || info.Mode().Perm() != 0660 {
		t.Fatalf("content normalization failed: %v %v", info, err)
	}
	remaining = 1000
	if err := normalizeSyncTree(context.Background(), directory.File, identity, uint32(os.Getuid()), 0, &remaining); err != nil {
		t.Fatal(err)
	}
}

func TestSyncWorkspaceRejectsHardLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	directory, err := workspace.OpenDirectory(root, workspace.PreparePayload{UserID: "user", WorkspaceID: "workspace", SyncSessionID: "sync"})
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	target := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(directory.Path, "hardlink")); err != nil {
		t.Fatal(err)
	}
	remaining := 1000
	if err := normalizeSyncTree(context.Background(), directory.File, runtimeIdentity{UID: os.Getuid(), GID: os.Getgid()}, uint32(os.Getuid()), 0, &remaining); err == nil {
		t.Fatal("hardlink accepted")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("hardlink permissions changed: %v %v", info, err)
	}
}

func TestSyncTraversePreservesExistingACL(t *testing.T) {
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	fd := int(directory.Fd())
	if err := unix.Fsetxattr(fd, "system.posix_acl_access", syncDataACL(2001, 2002, 7), 0); err != nil {
		t.Fatal(err)
	}
	if err := grantSyncTraverse(context.Background(), directory, 2001, 2003); err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	entries, err := readSyncACL(fd, stat.Mode)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[uint32]uint16{2001: 7, 2002: 7, 2003: 1}
	for _, entry := range entries {
		if entry.tag == 2 {
			permissions, ok := expected[entry.id]
			if !ok || permissions != entry.permissions {
				t.Fatalf("ACL changed: %#v", entries)
			}
			delete(expected, entry.id)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("ACL entries missing: %#v", expected)
	}
}

func TestSyncWorkspaceRuntimeIntegration(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_SYNC_RUNTIME_INTEGRATION") != "1" {
		t.Skip("requires disposable root Linux container")
	}
	if os.Geteuid() != 0 {
		t.Fatal("integration fixture requires root")
	}
	root := t.TempDir()
	engine := NewEngine(EngineConfig{WorkspaceRoot: root, NodeUser: "root", StateRoot: t.TempDir()})
	payload := map[string]any{"user_id": "sync-integration-user", "workspace_id": "workspace", "sync_session_id": "sync"}
	for iteration := 0; iteration < 2; iteration++ {
		result, err := engine.prepareWorkspace(context.Background(), payload)
		if err != nil {
			t.Fatal(err)
		}
		if result["status"] != "prepared" {
			t.Fatal(result)
		}
		identity, err := engine.lookupIdentity("sync-integration-user")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "sync-integration-user", "workspaces", "workspace", "files")
		var stat unix.Stat_t
		if err := unix.Stat(path, &stat); err != nil {
			t.Fatal(err)
		}
		if int(stat.Uid) != identity.UID || int(stat.Gid) != identity.GID || stat.Mode&0777 != 0770 {
			t.Fatalf("workspace owner/mode mismatch: %#v", stat)
		}
		if iteration == 0 {
			if err := os.WriteFile(filepath.Join(path, "new-content"), []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := unix.Stat(filepath.Join(path, "new-content"), &stat); err != nil {
				t.Fatal(err)
			}
			if int(stat.Uid) != identity.UID || stat.Mode&0777 != 0660 {
				t.Fatalf("new content not normalized on retry: %#v", stat)
			}
		}
	}
}

func TestSyncTraverseDoesNotUnmaskOtherPrincipals(t *testing.T) {
	directory, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	fd := int(directory.Fd())
	data := syncDataACL(2001, 2002, 7)
	group := make([]byte, 8)
	binary.LittleEndian.PutUint16(group, 0x08)
	binary.LittleEndian.PutUint16(group[2:], 7)
	binary.LittleEndian.PutUint32(group[4:], 2005)
	insertion := len(data) - 16
	data = append(append(append([]byte{}, data[:insertion]...), group...), data[insertion:]...)
	for offset := 4; offset < len(data); offset += 8 {
		if binary.LittleEndian.Uint16(data[offset:]) == 0x10 {
			binary.LittleEndian.PutUint16(data[offset+2:], 6)
		}
	}
	if err := unix.Fsetxattr(fd, "system.posix_acl_access", data, 0); err != nil {
		t.Fatal(err)
	}
	if err := grantSyncTraverse(context.Background(), directory, 2001, 2003); err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	entries, err := readSyncACL(fd, stat.Mode)
	if err != nil {
		t.Fatal(err)
	}
	mask := uint16(0)
	for _, entry := range entries {
		if entry.tag == 0x10 {
			mask = entry.permissions
		}
	}
	for _, entry := range entries {
		if entry.tag == 0x04 || entry.tag == 0x08 || (entry.tag == 0x02 && entry.id == 2002) {
			if entry.permissions&mask != 6 {
				t.Fatalf("unrelated principal gained permissions: %#v mask=%o", entry, mask)
			}
		}
	}
}
