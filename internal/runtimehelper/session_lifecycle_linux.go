package runtimehelper

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) openSessionLifecycleStore() (*os.File, error) {
	if err := validateID(e.config.NodeID, "node_id"); err != nil {
		return nil, err
	}
	root, err := skillmanager.OpenStateStore(filepath.Join(e.config.StateRoot, "session-lifecycle", "v1"), e.config.WorkspaceRoot, e.config.AccountRoot, e.config.SkillStateRoot)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	if err := e.verifyLifecycleScope(directory); err != nil {
		directory.Close()
		return nil, err
	}
	return directory, nil
}

func lifecycleFile(directory *os.File, name string, create bool) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Openat(int(directory.Fd()), name, flags, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		file.Close()
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 || stat.Uid != 0 || stat.Nlink != 1 || stat.Size > 4096 {
		file.Close()
		return nil, errors.New("unsafe session lifecycle file")
	}
	return file, nil
}

func lifecycleLock(ctx context.Context, directory *os.File, name string) (func(), error) {
	file, err := lifecycleFile(directory, name, true)
	if err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, err
	}
	if err := directory.Sync(); err != nil {
		file.Close()
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			file.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (e Engine) lockSessionLifecycle(ctx context.Context, session, kind string) (func(), error) {
	if err := validateID(session, "session_id"); err != nil {
		return nil, err
	}
	if kind != "runtime" && kind != "intent" {
		return nil, errors.New("invalid lifecycle lock")
	}
	directory, err := e.openSessionLifecycleStore()
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	return lifecycleLock(ctx, directory, lifecycleName(session, "."+kind+".lock"))
}

func (e Engine) readSessionStop(directory *os.File, session string) (bool, error) {
	file, err := lifecycleFile(directory, lifecycleName(session, ".stop.json"), false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return false, errors.New("invalid session stop record")
	}
	var intent sessionStopIntent
	if err := decodeStrictJSON(data, &intent); err != nil {
		return false, err
	}
	if err := intent.validate(e.config.NodeID, session); err != nil {
		return false, err
	}
	// A prior publisher may have failed its final sync. Reopening must establish
	// durability before treating that visible marker as an accepted stop intent.
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := directory.Sync(); err != nil {
		return false, err
	}
	return true, nil
}

func (e Engine) publishSessionStop(ctx context.Context, intent sessionStopIntent) error {
	if err := intent.validate(e.config.NodeID, intent.SessionID); err != nil {
		return err
	}
	directory, err := e.openSessionLifecycleStore()
	if err != nil {
		return err
	}
	defer directory.Close()
	unlock, err := lifecycleLock(ctx, directory, lifecycleName(intent.SessionID, ".intent.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	closed, err := e.readSessionStop(directory, intent.SessionID)
	if err != nil || closed {
		return err
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	temporary := ".intent-" + rand.Text()
	fd, err := unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer file.Close()
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := unix.Renameat2(int(directory.Fd()), temporary, int(directory.Fd()), lifecycleName(intent.SessionID, ".stop.json"), unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return directory.Sync()
}

func (e Engine) sessionStopState(ctx context.Context, session string) (bool, error) {
	directory, err := e.openSessionLifecycleStore()
	if err != nil {
		return false, err
	}
	defer directory.Close()
	unlock, err := lifecycleLock(ctx, directory, lifecycleName(session, ".intent.lock"))
	if err != nil {
		return false, err
	}
	defer unlock()
	return e.readSessionStop(directory, session)
}

func (e Engine) requireSessionOpen(ctx context.Context, session string) error {
	closed, err := e.sessionStopState(ctx, session)
	if err != nil {
		return err
	}
	if closed {
		return errSessionClosed
	}
	return nil
}

// submitSessionLaunch linearizes only command submission, not readiness or stopping.
func (e Engine) withSessionAdmission(ctx context.Context, session, kind string, submit func() error) error {
	if kind != "session" && kind != "tool" {
		return submit()
	}
	directory, err := e.openSessionLifecycleStore()
	if err != nil {
		return err
	}
	defer directory.Close()
	unlock, err := lifecycleLock(ctx, directory, lifecycleName(session, ".intent.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	closed, err := e.readSessionStop(directory, session)
	if err != nil {
		return err
	}
	if closed {
		return errSessionClosed
	}
	return submit()
}

func (e Engine) probeSessionLifecycle(ctx context.Context) bool {
	directory, err := e.openSessionLifecycleStore()
	if err != nil {
		return false
	}
	defer directory.Close()
	name := ".probe-" + rand.Text()
	if err := unix.Mkdirat(int(directory.Fd()), name, 0700); err != nil {
		return false
	}
	defer unix.Unlinkat(int(directory.Fd()), name, unix.AT_REMOVEDIR)
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	probe := os.NewFile(uintptr(fd), name)
	defer probe.Close()
	unlock, err := lifecycleLock(ctx, probe, "lock")
	if err != nil {
		return false
	}
	defer unix.Unlinkat(fd, "lock", 0)
	defer unlock()
	second, err := lifecycleFile(probe, "lock", false)
	if err != nil {
		return false
	}
	defer second.Close()
	if err := unix.Flock(int(second.Fd()), unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
		return false
	}
	file, err := lifecycleFile(probe, "candidate", true)
	if err != nil {
		return false
	}
	defer unix.Unlinkat(fd, "candidate", 0)
	defer unix.Unlinkat(fd, "published", 0)
	_, writeErr := file.Write([]byte("lifecycle-probe-v1\n"))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return false
	}
	if err := unix.Renameat2(fd, "candidate", fd, "published", unix.RENAME_NOREPLACE); err != nil {
		return false
	}
	if err := publishLifecycleFile(probe, "published", []byte("replacement")); !errors.Is(err, unix.EEXIST) {
		return false
	}
	published, err := lifecycleFile(probe, "published", false)
	if err != nil {
		return false
	}
	data, readErr := io.ReadAll(io.LimitReader(published, 64))
	closeErr = published.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(data, []byte("lifecycle-probe-v1\n")) {
		return false
	}
	return probe.Sync() == nil && directory.Sync() == nil
}

func (e Engine) verifyLifecycleScope(directory *os.File) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	unlock, err := lifecycleLock(ctx, directory, "scope.lock")
	if err != nil {
		return err
	}
	defer unlock()
	expected, err := json.Marshal(struct {
		Version int    `json:"version"`
		NodeID  string `json:"node_id"`
	}{1, e.config.NodeID})
	if err != nil {
		return err
	}
	file, err := lifecycleFile(directory, "scope.json", false)
	if errors.Is(err, os.ErrNotExist) {
		return publishLifecycleFile(directory, "scope.json", expected)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || !bytes.Equal(data, expected) {
		return errors.New("session lifecycle store belongs to a different Node or version")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return directory.Sync()
}

func publishLifecycleFile(directory *os.File, name string, data []byte) error {
	temporary := ".publish-" + rand.Text()
	fd, err := unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer file.Close()
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	if _, err := file.Write(data); err != nil {
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

func (e Engine) submitSessionLaunch(ctx context.Context, session, kind string, submit func() error) error {
	err := e.withSessionAdmission(ctx, session, kind, submit)
	if err == nil && (kind == "session" || kind == "tool") && e.execution != nil {
		e.execution.FreshAdmission = true
	}
	return err
}
