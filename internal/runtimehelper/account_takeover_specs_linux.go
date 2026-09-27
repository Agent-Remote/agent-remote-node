package runtimehelper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func (e Engine) openTakeoverRuntimeRoot(category string) (*os.Root, error) {
	if _, err := os.Lstat(e.config.StateRoot); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(e.config.StateRoot)
	if err != nil {
		return nil, errTakeoverWritersUnknown
	}
	anchor, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, err
	}
	defer anchor.Close()
	file, err := anchor.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fd, err := unix.Openat(int(file.Fd()), category, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err := trustedTakeoverMode(fd, true); err != nil {
		return nil, err
	}
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}

func trustedTakeoverMode(fd int, directory bool) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	kind := uint32(unix.S_IFREG)
	if directory {
		kind = unix.S_IFDIR
	}
	if stat.Mode&unix.S_IFMT != kind || stat.Uid != 0 || stat.Mode&0o022 != 0 || !directory && stat.Size > 128<<10 {
		return errTakeoverWritersUnknown
	}
	return nil
}

func readTakeoverSpec(root *os.Root, name string, destination any) error {
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	parts := strings.Split(name, "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			_ = parent.Close()
			return errTakeoverWritersUnknown
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		directory := index < len(parts)-1
		if directory {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(int(parent.Fd()), part, flags, 0)
		_ = parent.Close()
		if err != nil {
			return err
		}
		parent = os.NewFile(uintptr(fd), "takeover-spec")
		if err := trustedTakeoverMode(fd, directory); err != nil {
			_ = parent.Close()
			return err
		}
	}
	defer parent.Close()
	data, err := io.ReadAll(io.LimitReader(parent, (128<<10)+1))
	if err != nil || len(data) > 128<<10 {
		return errTakeoverWritersUnknown
	}
	return decodeStrictJSON(data, destination)
}

func (e Engine) readNativeTakeoverSpec(root *os.Root, id string) (SessionSpec, error) {
	var spec SessionSpec
	if err := readTakeoverSpec(root, id+"/spec.json", &spec); err != nil {
		return spec, err
	}
	config, err := runtimeConfigForSpec(e.config, spec)
	if err != nil {
		return spec, err
	}
	return spec, validateSessionSpec(config, spec, e.specPath(id))
}

func (e Engine) readDockerTakeoverSpec(root *os.Root, id string) (DockerSessionSpec, error) {
	if !validSkillUUID(id) {
		return DockerSessionSpec{}, errTakeoverWritersUnknown
	}
	name := id + "/spec.json"
	if _, err := root.Lstat(id); errors.Is(err, os.ErrNotExist) {
		name = id + ".json"
	} else if err != nil {
		return DockerSessionSpec{}, err
	}
	var spec DockerSessionSpec
	if err := readTakeoverSpec(root, filepath.ToSlash(name), &spec); err != nil {
		return spec, err
	}
	return spec, e.validateDockerSessionSpec(spec, id)
}
