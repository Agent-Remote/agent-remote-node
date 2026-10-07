package runtimehelper

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func verifyTemporaryMount(spec SessionSpec, parent *os.File, target string) error {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(target, &filesystem); err != nil {
		return err
	}
	if spec.Policy.TemporaryStorage == "" || spec.Policy.TemporaryStorage == "tmpfs" {
		if filesystem.Type != unix.TMPFS_MAGIC {
			return errors.New("runtime tmp mount has unexpected filesystem")
		}
		return nil
	}
	if spec.Policy.TemporaryStorage != "disk" || filesystem.Type != unix.EXT4_SUPER_MAGIC {
		return errors.New("runtime tmp mount has unexpected filesystem")
	}
	var backing, mount unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), temporaryImageName, &backing, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if backing.Mode&unix.S_IFMT != unix.S_IFREG || backing.Uid != 0 || backing.Mode&0o7777 != 0o600 || backing.Nlink != 1 || backing.Size != spec.Policy.TemporarySizeBytes {
		return errors.New("temporary image identity or permissions changed")
	}
	if err := unix.Stat(target, &mount); err != nil {
		return err
	}
	if unix.Major(uint64(mount.Dev)) != 7 {
		return errors.New("temporary filesystem is not a loop device")
	}
	device, err := os.Open(fmt.Sprintf("/dev/loop%d", unix.Minor(uint64(mount.Dev))))
	if err != nil {
		return err
	}
	defer device.Close()
	loop, err := unix.IoctlLoopGetStatus64(int(device.Fd()))
	if err != nil {
		return err
	}
	if loop.Device != uint64(backing.Dev) || loop.Inode != backing.Ino || loop.Offset != 0 || loop.Sizelimit != 0 {
		return errors.New("temporary mount no longer belongs to its original image")
	}
	return nil
}
