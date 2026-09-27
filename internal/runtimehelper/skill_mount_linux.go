package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/managedskills"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) mountNativeSkills(ctx context.Context, spec SessionSpec) error {
	if spec.SkillSnapshotID == "" {
		return nil
	}
	bundle, receipt, err := e.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if err := e.validateNativeSkillBinding(spec, receipt.Snapshot.Binding); err != nil {
		return err
	}
	if receipt.Runtime.BootID != spec.BootID || receipt.Runtime.UID != spec.RuntimeUID || receipt.Runtime.GID != spec.RuntimeGID {
		return errors.New("skill mount runtime identity mismatch")
	}
	if err := verifySkillSystemPins(spec, receipt.Snapshot.Binding.SystemReleases); err != nil {
		return err
	}
	if _, err := skillmanager.ReadFinalization(bundle, receipt.Snapshot.Binding); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("finalized or corrupt skill snapshot cannot be relaunched")
	}
	if _, err := skillmanager.ReadTermination(bundle, receipt.Snapshot.Binding); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("terminated or corrupt skill snapshot cannot be relaunched")
	}
	if err := verifyNativePython(ctx, spec.RuntimeUID, spec.RuntimeGID, receipt.Snapshot.Capture.RuntimeDependencies); err != nil {
		return err
	}
	work, err := skillmanager.OpenSessionWork(bundle, receipt.Runtime)
	if err != nil {
		return err
	}
	defer work.Close()
	parent, err := openSkillMountParent(spec)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := unix.Mkdirat(int(parent.Fd()), "skill-work", 0o000); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	target := fmt.Sprintf("/proc/self/fd/%d/skill-work", parent.Fd())
	mounted, err := skillMountPresent(parent)
	if err != nil {
		return err
	}
	if mounted {
		if err := managedskills.VerifySessionSkills(filepath.Join(spec.SessionRoot, "system-skills"), spec.DeviceControlProtocolVersion != 0); err != nil {
			return err
		}
		if err := sameSkillMount(work, target); err != nil {
			return err
		}
		return hardenSkillMount(target)
	}
	if err := prepareSessionSystemSkills(spec); err != nil {
		return err
	}
	var before unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), "skill-work", &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if before.Mode&unix.S_IFMT != unix.S_IFDIR || before.Uid != 0 || before.Mode&0o7777 != 0 {
		return errors.New("skill mount target is not a protected empty directory")
	}
	if err := unix.Mount(fmt.Sprintf("/proc/self/fd/%d", work.Fd()), target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind private skill work: %w", err)
	}
	if err := hardenSkillMount(target); err != nil {
		return err
	}
	return sameSkillMount(work, target)
}

func hardenSkillMount(target string) error {
	// A failed hardening step leaves the mount in place for checked cleanup; never delete through it.
	if err := unix.Mount("", target, "", unix.MS_PRIVATE, ""); err != nil {
		return err
	}
	if err := unix.Mount("", target, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		return err
	}
	return nil
}

func (e Engine) cleanupNativeSkillMount(spec SessionSpec) error {
	parent, err := openSkillMountParent(spec)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	mounted, err := skillMountPresent(parent)
	if err != nil || !mounted {
		return err
	}
	bundle, receipt, err := e.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		return err
	}
	defer bundle.Close()
	work, err := skillmanager.OpenSessionWork(bundle, receipt.Runtime)
	if err != nil {
		return err
	}
	defer work.Close()
	target := fmt.Sprintf("/proc/self/fd/%d/skill-work", parent.Fd())
	if err := sameSkillMount(work, target); err != nil {
		return err
	}
	// No lazy detach: an uncertain/busy mount must keep the session directory intact.
	if err := unix.Unmount(target, 0); err != nil {
		return fmt.Errorf("unmount retained skill work before cleanup: %w", err)
	}
	mounted, err = skillMountPresent(parent)
	if err != nil {
		return err
	}
	if mounted {
		return errors.New("skill work mount remains attached; preserving session directory")
	}
	return nil
}

func openSkillMountParent(spec SessionSpec) (*os.File, error) {
	fd, err := unix.Open(spec.SessionRoot, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "skill-mount-parent")
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return nil, err
	}
	if stat.Uid != 0 || stat.Mode&0o022 != 0 {
		_ = file.Close()
		return nil, errors.New("skill mount parent must be protected and root-owned")
	}
	return file, nil
}

func skillMountPresent(parent *os.File) (bool, error) {
	return sessionMountPresent(parent, "skill-work")
}

func sessionMountPresent(parent *os.File, name string) (bool, error) {
	var directory, target unix.Statx_t
	if err := unix.Statx(int(parent.Fd()), ".", unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &directory); err != nil {
		return false, err
	}
	err := unix.Statx(int(parent.Fd()), name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID|unix.STATX_TYPE, &target)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if directory.Mask&unix.STATX_MNT_ID == 0 || target.Mask&unix.STATX_MNT_ID == 0 || target.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false, errors.New("skill mount identity cannot be verified")
	}
	return directory.Mnt_id != target.Mnt_id, nil
}

func sameSkillMount(work *os.File, target string) error {
	expected, err := work.Stat()
	if err != nil {
		return err
	}
	actual, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, actual) {
		return errors.New("skill mount points at an unexpected directory")
	}
	return nil
}

func prepareSessionSystemSkills(spec SessionSpec) error {
	root := filepath.Join(spec.SessionRoot, "system-skills")
	if err := ensureRootDirectory(root, 0o700); err != nil {
		return err
	}
	if err := managedskills.InstallClaude(root, nil); err != nil {
		return err
	}
	// These bytes come from release-verified embedded sources, never the mutable account directory.
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o555)
		}
		if !entry.Type().IsRegular() {
			return errors.New("managed system skill contains an unexpected entry")
		}
		return os.Chmod(path, 0o444)
	}); err != nil {
		return err
	}
	return managedskills.VerifySessionSkills(root, spec.DeviceControlProtocolVersion != 0)
}

// Recovery inspects an existing mount; it must never mount or repair files beneath a live runtime.
func (e Engine) verifyExistingManagedMount(spec SessionSpec) error {
	bundle, receipt, err := e.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if err := verifySkillSystemPins(spec, receipt.Snapshot.Binding.SystemReleases); err != nil {
		return err
	}
	work, err := skillmanager.OpenSessionWork(bundle, receipt.Runtime)
	if err != nil {
		return err
	}
	defer work.Close()
	parent, err := openSkillMountParent(spec)
	if err != nil {
		return err
	}
	defer parent.Close()
	mounted, err := skillMountPresent(parent)
	if err != nil {
		return err
	}
	if !mounted {
		return errors.New("managed launch recovery requires its original mounted work")
	}
	if err := sameSkillMount(work, fmt.Sprintf("/proc/self/fd/%d/skill-work", parent.Fd())); err != nil {
		return err
	}
	if err := verifySkillMountFlags(parent); err != nil {
		return err
	}
	return managedskills.VerifySessionSkills(filepath.Join(spec.SessionRoot, "system-skills"), spec.DeviceControlProtocolVersion != 0)
}
