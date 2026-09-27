package skillmanager

import (
	"crypto/rand"
	"errors"
	"os"
)

// ProbeStateStore checks the real private volume's reserve and durable write/rename/read path.
// Only a fresh probe file is touched; account content and retained journals remain unchanged.
func ProbeStateStore(root *os.Root, policy StatePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := verifyPrivateStore(directory); err != nil {
		return err
	}
	if err := checkDiskSpace(directory, Manifest{}, CopyPolicy{MinimumFreeBytes: policy.MinimumFreeBytes, ReservePercent: policy.ReservePercent}); err != nil {
		return err
	}
	name := "probe-" + rand.Text()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	defer func() { _ = root.Remove(name); _ = directory.Sync() }()
	if _, err := file.Write([]byte("skill-storage-probe-v1")); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(name, name+"-ready"); err != nil {
		return err
	}
	name += "-ready"
	if err := directory.Sync(); err != nil {
		return err
	}
	data, err := root.ReadFile(name)
	if err != nil {
		return err
	}
	if string(data) != "skill-storage-probe-v1" {
		return errors.New("skill storage probe content mismatch")
	}
	if err := root.Remove(name); err != nil {
		return err
	}
	return directory.Sync()
}
