package skillmanager

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// OpenAccountCapture verifies the immutable binding and complete manifest, never opening the source.
// Only an absent bundle returns os.ErrNotExist; damage inside a published bundle cannot recapture.
func OpenAccountCapture(store *os.Root, binding AccountTakeoverBinding) (*os.Root, AccountCapture, Manifest, error) {
	if err := binding.Validate(); err != nil {
		return nil, AccountCapture{}, Manifest{}, err
	}
	if err := requireCaptureFence(store, binding); err != nil {
		return nil, AccountCapture{}, Manifest{}, errors.New("account capture fence is missing or inconsistent")
	}
	directory, err := privateBundleFile(store)
	if err != nil {
		return nil, AccountCapture{}, Manifest{}, err
	}
	_ = directory.Close()
	name := accountCaptureName(binding.AccountID)
	info, err := store.Lstat(name)
	if err != nil {
		return nil, AccountCapture{}, Manifest{}, err
	}
	if !info.IsDir() {
		return nil, AccountCapture{}, Manifest{}, errors.New("account capture bundle is not a directory")
	}
	bundle, err := store.OpenRoot(name)
	if err != nil {
		return nil, AccountCapture{}, Manifest{}, errors.New("account capture bundle cannot be opened")
	}
	receipt, manifest, err := readAccountCapture(bundle, binding)
	if err != nil {
		_ = bundle.Close()
		return nil, AccountCapture{}, Manifest{}, errors.New("account capture metadata is incomplete or invalid")
	}
	return bundle, receipt, manifest, nil
}

func readAccountCapture(bundle *os.Root, binding AccountTakeoverBinding) (AccountCapture, Manifest, error) {
	var receipt AccountCapture
	if err := readPrivateJSON(bundle, "record.json", 1<<20, &receipt); err != nil {
		return AccountCapture{}, Manifest{}, err
	}
	if receipt.Version != 1 || receipt.Binding != binding || !validSnapshotID(receipt.HelperReceiptID) || !contentDigestPattern.MatchString(receipt.TreeDigest) {
		return AccountCapture{}, Manifest{}, errors.New("invalid account capture receipt")
	}
	var manifest Manifest
	if err := readPrivateJSON(bundle, "manifest.json", maxManifestBytes, &manifest); err != nil {
		return AccountCapture{}, Manifest{}, err
	}
	digest, err := Digest(manifest)
	if err != nil || digest != receipt.TreeDigest || !receipt.SourceExists && len(manifest.Entries) != 0 {
		return AccountCapture{}, Manifest{}, errors.New("account capture manifest differs from its receipt")
	}
	for _, entry := range manifest.Entries {
		if entry.Kind == "runtime_link" || reservedAccountPath(entry.Path) {
			return AccountCapture{}, Manifest{}, errors.New("account capture includes unverified or system content")
		}
	}
	return receipt, manifest, nil
}

// OpenAccountCaptureObject opens only a digest retained by this exact capture for streaming.
// Callers must verify streamed bytes against the returned entry before accepting remote persistence.
func OpenAccountCaptureObject(store *os.Root, binding AccountTakeoverBinding, digest string) (*os.File, Entry, error) {
	bundle, _, manifest, err := OpenAccountCapture(store, binding)
	if err != nil {
		return nil, Entry{}, err
	}
	defer bundle.Close()
	var expected Entry
	for _, entry := range manifest.Entries {
		if entry.Kind == "file" && entry.SHA256 == digest {
			expected = entry
			break
		}
	}
	if expected.SHA256 == "" {
		return nil, Entry{}, errors.New("digest is not part of the retained account capture")
	}
	parent, err := privateBundleFile(bundle)
	if err != nil {
		return nil, Entry{}, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), "objects", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Entry{}, err
	}
	objects := os.NewFile(uintptr(fd), "objects")
	defer objects.Close()
	if err := verifyPrivateStore(objects); err != nil {
		return nil, Entry{}, err
	}
	fd, err = unix.Openat(int(objects.Fd()), digest, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Entry{}, err
	}
	file := os.NewFile(uintptr(fd), digest)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, Entry{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size != expected.Size {
		_ = file.Close()
		return nil, Entry{}, errors.New("unsafe account capture object")
	}
	return file, expected, nil
}

func reservedAccountPath(path string) bool {
	root, _, _ := strings.Cut(path, "/")
	return root == "ego-browser" || root == "agent-remote-device"
}
