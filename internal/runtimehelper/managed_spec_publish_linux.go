package runtimehelper

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) saveManagedSpec(spec SessionSpec) error {
	if err := e.requireManagedSpecIdentity(spec); err != nil {
		return err
	}
	if err := validateSessionSpec(e.config, spec, e.specPath(spec.SessionID)); err != nil {
		return err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	if err := skillmanager.PublishSessionSpecDraft(store, e.managedSpecIntent(spec), data); err != nil {
		return err
	}
	directory, err := openSkillMountParent(spec)
	if err != nil {
		return err
	}
	defer directory.Close()
	resolvers := make([]string, 0, len(e.config.DNSResolvers))
	for _, resolver := range e.config.DNSResolvers {
		resolvers = append(resolvers, "nameserver "+resolver)
	}
	for _, file := range []struct{ name, content string }{
		{"resolv.conf", strings.Join(resolvers, "\n") + "\n"},
		{"timezone", spec.Timezone + "\n"},
		{"spec.json", string(data) + "\n"},
	} {
		if err := publishManagedSpecFile(directory, file.name, []byte(file.content)); err != nil {
			return err
		}
	}
	return nil
}

func publishManagedSpecFile(directory *os.File, name string, data []byte) error {
	if err := verifyManagedSpecFile(directory, name, data); err == nil {
		return directory.Sync()
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	temporary := ".spec-" + rand.Text()
	fd, err := unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer file.Close()
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := unix.Renameat2(int(directory.Fd()), temporary, int(directory.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return directory.Sync()
}

func verifyManagedSpecFile(directory *os.File, name string, data []byte) error {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o644 || stat.Uid != 0 || stat.Nlink != 1 || stat.Size != int64(len(data)) {
		return errors.New("existing managed spec file is unsafe or different")
	}
	old, err := io.ReadAll(io.LimitReader(file, int64(len(data))+1))
	if err != nil || !bytes.Equal(old, data) {
		return errors.New("existing managed spec file does not match the original draft")
	}
	return file.Sync()
}

func (e Engine) verifyManagedSpecFiles(spec SessionSpec) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	directory, err := openSkillMountParent(spec)
	if err != nil {
		return err
	}
	defer directory.Close()
	resolvers := make([]string, 0, len(e.config.DNSResolvers))
	for _, resolver := range e.config.DNSResolvers {
		resolvers = append(resolvers, "nameserver "+resolver)
	}
	for name, content := range map[string]string{
		"resolv.conf": strings.Join(resolvers, "\n") + "\n",
		"timezone":    spec.Timezone + "\n",
		"spec.json":   string(data) + "\n",
	} {
		if err := verifyManagedSpecFile(directory, name, []byte(content)); err != nil {
			return err
		}
	}
	return nil
}

// verifyRetainedSpecFile accepts an absent transient spec, but never changed original bytes.
func verifyRetainedSpecFile(spec SessionSpec) error {
	directory, err := openSkillMountParent(spec)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	// Do not validate today's wrapper release or recreate missing files during data recovery.
	err = verifyManagedSpecFile(directory, "spec.json", append(data, '\n'))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
