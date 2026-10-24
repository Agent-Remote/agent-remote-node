//go:build linux || darwin

package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// OpenDirectory pins each untrusted descendant without following symbolic links.
func OpenDirectory(root string, payload PreparePayload) (_ *Directory, resultErr error) {
	path, err := resolveRemotePath(root, payload)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// The configured root is administrator-controlled; descendants are untrusted.
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	trustedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	rootFD, err := unix.Open(trustedRoot, flags, 0)
	if err != nil {
		return nil, err
	}
	parentFD, err := unix.Open(filepath.Dir(trustedRoot), flags, 0)
	if err != nil {
		_ = unix.Close(rootFD)
		return nil, err
	}
	directory := &Directory{Path: path, File: os.NewFile(uintptr(rootFD), root), Parents: []*os.File{os.NewFile(uintptr(parentFD), filepath.Dir(root))}}
	defer func() {
		if resultErr != nil {
			directory.Close()
		}
	}()
	for _, part := range []string{payload.UserID, "workspaces", payload.WorkspaceID, "files"} {
		parent := int(directory.File.Fd())
		if err := unix.Mkdirat(parent, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
		fd, err := unix.Openat(parent, part, flags, 0)
		if err != nil {
			return nil, err
		}
		directory.Parents = append(directory.Parents, directory.File)
		directory.File = os.NewFile(uintptr(fd), part)
	}
	return directory, nil
}

// WriteMarker atomically replaces a marker without modifying an existing link target.
func (d *Directory) WriteMarker(payload PreparePayload) (PrepareResult, error) {
	if err := validatePayload(payload); err != nil {
		return PrepareResult{}, err
	}
	data, err := json.MarshalIndent(map[string]string{
		"user_id": payload.UserID, "workspace_id": payload.WorkspaceID,
		"sync_session_id": payload.SyncSessionID, "prepared_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return PrepareResult{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return PrepareResult{}, err
	}
	temporary := ".agent-remote-marker-" + hex.EncodeToString(nonce[:])
	parent := int(d.File.Fd())
	fd, err := unix.Openat(parent, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return PrepareResult{}, err
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer func() { _ = file.Close(); _ = unix.Unlinkat(parent, temporary, 0) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return PrepareResult{}, err
	}
	if err := file.Sync(); err != nil {
		return PrepareResult{}, err
	}
	if err := file.Close(); err != nil {
		return PrepareResult{}, err
	}
	const marker = ".agent-remote-workspace.json"
	if err := unix.Renameat(parent, temporary, parent, marker); err != nil {
		return PrepareResult{}, err
	}
	if err := unix.Fsync(parent); err != nil {
		return PrepareResult{}, err
	}
	return PrepareResult{RemotePath: d.Path, MarkerPath: filepath.Join(d.Path, marker)}, nil
}
