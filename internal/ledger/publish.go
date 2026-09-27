package ledger

import (
	"errors"
	"os"
	"path/filepath"
)

// publish reports whether the destination changed even if its directory sync failed.
func publish(path string, data []byte) (bool, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false, err
	}
	existing, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err == nil && !existing.Mode().IsRegular() {
		return false, errors.New("ledger path must be a regular file")
	}
	parent, err := os.Open(directory)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	temporary, err := os.CreateTemp(directory, ".agent-remote-ledger-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.Write(data); err != nil {
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return false, err
	}
	return true, parent.Sync()
}
