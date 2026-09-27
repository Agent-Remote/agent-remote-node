package runtimehelper

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var nativePythonName = regexp.MustCompile(`^python3(?:\.[0-9]{1,2})?$`)
var nativePythonIdentity = regexp.MustCompile(`^cpython 3 ([0-9]{1,2}) ([a-z]{0,4}) (little|big)\n$`)

const nativePythonProbe = `import sys; print(sys.implementation.name, sys.version_info.major, sys.version_info.minor, sys.abiflags, sys.byteorder)`

// Discovery has no caller-selected command or search path. Unavailable optional interpreters
// are omitted; materialization separately refuses every required link that was not verified.
func discoverNativePython(ctx context.Context, uid, gid int) (map[string]string, error) {
	if uid <= 0 || gid <= 0 || uid > math.MaxInt32 || gid > math.MaxInt32 {
		return nil, errors.New("runtime dependency probe requires a non-root identity")
	}
	dependencies := make(map[string]string)
	for _, target := range []string{"/usr/bin/python3", "/usr/local/bin/python3"} {
		resolved, file, err := openNativePython(target)
		if err != nil {
			continue
		}
		before, beforeErr := file.Stat()
		identity, err := probeNativePython(ctx, resolved, uid, gid)
		if err == nil {
			// Package replacement during the probe cannot certify a different executable.
			current, verify, verifyErr := openNativePython(target)
			if verifyErr == nil {
				after, afterErr := verify.Stat()
				if beforeErr == nil && afterErr == nil && current == resolved && os.SameFile(before, after) &&
					before.Size() == after.Size() && before.ModTime() == after.ModTime() {
					for _, path := range []string{target, resolved} {
						dependencies[nativePythonDependency(identity, path)] = path
					}
				}
				_ = verify.Close()
			}
		}
		_ = file.Close()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(dependencies) == 0 {
		return nil, ctx.Err()
	}
	return dependencies, ctx.Err()
}

func nativePythonDependency(identity, target string) string {
	return identity + "-" + strings.ReplaceAll(strings.TrimPrefix(target, "/"), "/", "-")
}

func probeNativePython(ctx context.Context, executable string, uid, gid int) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, executable, "-I", "-S", "-c", nativePythonProbe)
	command.Dir = "/"
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	command.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)},
		Pdeathsig:  unix.SIGKILL,
	}
	output := &pythonProbeOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return "", errors.New("runtime dependency interpreter probe failed")
	}
	fields := nativePythonIdentity.FindStringSubmatch(output.String())
	if fields == nil {
		return "", errors.New("runtime dependency interpreter identity is unsupported")
	}
	return fmt.Sprintf("cpython-3.%s%s-%s-linux-%s", fields[1], fields[2], fields[3], runtime.GOARCH), nil
}

// Do not embed bytes.Buffer: its promoted ReadFrom would let io.Copy bypass this bound.
type pythonProbeOutput struct{ buffer bytes.Buffer }

func (output *pythonProbeOutput) Write(value []byte) (int, error) {
	if len(value) > 128-output.buffer.Len() {
		return 0, errors.New("runtime dependency probe output exceeds limit")
	}
	return output.buffer.Write(value)
}

func (output *pythonProbeOutput) String() string { return output.buffer.String() }

// Open each ancestor without following links. Interpreter symlinks may only resolve to another
// Python name in the same protected directory, never into account, runtime or imported content.
func openNativePython(target string) (string, *os.File, error) {
	directory, name := filepath.Dir(target), filepath.Base(target)
	if target != filepath.Clean(target) || (directory != "/usr/bin" && directory != "/usr/local/bin") || !nativePythonName.MatchString(name) {
		return "", nil, errors.New("unsupported runtime dependency path")
	}
	parent, err := os.Open("/")
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = parent.Close() }()
	for _, component := range strings.Split(strings.TrimPrefix(directory, "/"), "/") {
		fd, err := unix.Openat(int(parent.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return "", nil, err
		}
		_ = parent.Close()
		parent = os.NewFile(uintptr(fd), component)
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil || stat.Uid != 0 || stat.Mode&0o022 != 0 {
			return "", nil, errors.New("unsafe runtime dependency directory")
		}
	}
	resolved, file, err := openNativePythonFile(parent, name)
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(directory, resolved), file, nil
}

func openNativePythonFile(parent *os.File, name string) (string, *os.File, error) {
	for range 8 {
		var stat unix.Stat_t
		if err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return "", nil, err
		}
		if stat.Uid != 0 {
			return "", nil, errors.New("runtime dependency is not root-owned")
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			var buffer [256]byte
			count, err := unix.Readlinkat(int(parent.Fd()), name, buffer[:])
			if err != nil || count == len(buffer) || !nativePythonName.MatchString(string(buffer[:count])) {
				return "", nil, errors.New("unsupported runtime dependency link")
			}
			name = string(buffer[:count])
			continue
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return "", nil, errors.New("runtime dependency is not a regular executable")
		}
		fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return "", nil, err
		}
		file := os.NewFile(uintptr(fd), name)
		var actual unix.Stat_t
		if err := unix.Fstat(fd, &actual); err != nil || actual.Dev != stat.Dev || actual.Ino != stat.Ino ||
			actual.Uid != 0 || actual.Mode&unix.S_IFMT != unix.S_IFREG || actual.Mode&0o6022 != 0 || actual.Mode&0o005 != 0o005 {
			_ = file.Close()
			return "", nil, errors.New("unsafe runtime dependency executable")
		}
		header, err := elf.NewFile(file)
		machine, supported := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
		if err != nil || !supported || header.Machine != machine || header.Class != elf.ELFCLASS64 || header.Data != elf.ELFDATA2LSB {
			_ = file.Close()
			return "", nil, errors.New("unsafe runtime dependency executable")
		}
		return name, file, nil
	}
	return "", nil, errors.New("runtime dependency link depth exceeded")
}

func verifyNativePython(ctx context.Context, uid, gid int, expected map[string]string) error {
	if len(expected) == 0 {
		return ctx.Err()
	}
	available, err := discoverNativePython(ctx, uid, gid)
	if err != nil {
		return err
	}
	for dependency, target := range expected {
		if available[dependency] != target {
			return errors.New("runtime_dependency_missing: prepared Python interpreter is unavailable or changed")
		}
	}
	return nil
}
