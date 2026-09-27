package skillmanager

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func validateSnapshot(snapshot PreparedSnapshot) error {
	if snapshot.Version != 1 {
		return errors.New("unsupported prepared skill snapshot version")
	}
	if err := snapshot.Binding.Validate(); err != nil {
		return err
	}
	_, _, err := validateCaptureOptions(snapshot.Capture)
	return err
}

func loadPreparedSnapshot(bundle *os.Root, binding SnapshotBinding) (PreparedSnapshot, error) {
	file, err := privateBundleFile(bundle)
	if err != nil {
		return PreparedSnapshot{}, err
	}
	_ = file.Close()
	var snapshot PreparedSnapshot
	if err := readPrivateJSON(bundle, "snapshot.json", 1<<20, &snapshot); err != nil {
		return PreparedSnapshot{}, err
	}
	if err := validateSnapshot(snapshot); err != nil {
		return PreparedSnapshot{}, err
	}
	if snapshot.Binding != binding {
		return PreparedSnapshot{}, errors.New("skill snapshot authorization binding mismatch")
	}
	return snapshot, nil
}

func privateBundleFile(bundle *os.Root) (*os.File, error) {
	file, err := bundle.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	if err := verifyPrivateStore(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func readPrivateJSON(root *os.Root, name string, limit int64, result any) error {
	if name == "." || name == ".." || filepath.Base(name) != name {
		return errors.New("invalid skill journal name")
	}
	directory, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	// os.Root resolves leaf links itself; openat must enforce O_NOFOLLOW at the kernel boundary.
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || before.Size() > limit {
		return errors.New("unsafe or oversized skill journal file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("skill journal contains trailing data")
	}
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameSourceInfo(before, after) {
		return errors.New("skill journal changed during read")
	}
	return nil
}

func writePrivateJSON(root *os.Root, directory *os.File, name string, value any, noReplace bool) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("skill journal record exceeds metadata limit")
	}
	temporary := ".record-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	err = errors.Join(writeComplete(file, data), file.Sync(), file.Close())
	if err != nil {
		return err
	}
	flags := uint(0)
	if noReplace {
		flags = unix.RENAME_NOREPLACE
	}
	if err := unix.Renameat2(int(directory.Fd()), temporary, int(directory.Fd()), name, flags); err != nil {
		return err
	}
	return directory.Sync()
}
