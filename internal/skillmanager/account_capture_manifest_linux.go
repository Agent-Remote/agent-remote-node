package skillmanager

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// OpenAccountCaptureManifest opens a read-only manifest from an already verified capture.
// Receivers must decode it and compare its canonical digest with the returned receipt.
func OpenAccountCaptureManifest(store *os.Root, binding AccountTakeoverBinding) (*os.File, AccountCapture, error) {
	bundle, receipt, _, err := OpenAccountCapture(store, binding)
	if err != nil {
		return nil, AccountCapture{}, err
	}
	defer bundle.Close()
	directory, err := privateBundleFile(bundle)
	if err != nil {
		return nil, AccountCapture{}, err
	}
	defer directory.Close()
	fd, err := unix.Openat(int(directory.Fd()), "manifest.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, AccountCapture{}, err
	}
	file := os.NewFile(uintptr(fd), "capture-manifest")
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, AccountCapture{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size <= 0 || stat.Size > maxManifestBytes {
		_ = file.Close()
		return nil, AccountCapture{}, errors.New("unsafe account capture manifest")
	}
	return file, receipt, nil
}
