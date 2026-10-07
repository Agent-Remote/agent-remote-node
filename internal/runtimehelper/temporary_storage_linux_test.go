package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDiskTemporaryStorageIntegration(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_TEMP_STORAGE_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires an isolated root Linux mount namespace with loop devices")
	}
	for _, size := range []int64{16 << 30, 64 << 20} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			root, err := os.MkdirTemp("/var/tmp", "ar-disk-temp-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			if err := os.Chmod(root, 0o711); err != nil {
				t.Fatal(err)
			}
			spec := SessionSpec{SessionRoot: root, RuntimeUID: 65534, RuntimeGID: 65534,
				Policy: RuntimePolicy{TemporaryStorage: "disk", TemporarySizeBytes: size}}
			engine := NewEngine(EngineConfig{})
			if err := engine.setupTemp(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := engine.cleanupTemp(context.Background(), spec); err != nil {
					t.Error(err)
				}
			})
			mount := filepath.Join(root, "tmp")
			var stat unix.Statfs_t
			if err := unix.Statfs(mount, &stat); err != nil || stat.Type != unix.EXT4_SUPER_MAGIC {
				t.Fatal("not a disk filesystem", err, stat.Type)
			}
			capacity := int64(stat.Blocks) * int64(stat.Bsize)
			if capacity > size || capacity < size*3/4 || stat.Flags&unix.ST_NOSUID == 0 || stat.Flags&unix.ST_NODEV == 0 {
				t.Fatal("capacity or mount restrictions changed", stat)
			}
			owner := exec.Command("touch", filepath.Join(mount, "owned"))
			owner.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}}}
			if err := owner.Run(); err != nil {
				t.Fatal("runtime owner cannot write temporary files", err)
			}
			stranger := exec.Command("touch", filepath.Join(mount, "foreign"))
			stranger.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65533, Gid: 65533, Groups: []uint32{65533}}}
			if err := stranger.Run(); err == nil {
				t.Fatal("another runtime identity wrote into private temporary space")
			}
			file, err := os.Create(filepath.Join(mount, "capacity"))
			if err != nil {
				t.Fatal(err)
			}
			amount := int64(1100 << 20)
			if size < amount {
				amount = size + 1
			}
			err = unix.Fallocate(int(file.Fd()), 0, 0, amount)
			_ = file.Close()
			if size > 1<<30 && err != nil {
				t.Fatal("cannot allocate more than the old 1 GiB cap", err)
			}
			if size < amount && !errors.Is(err, unix.ENOSPC) {
				t.Fatal("filesystem failed to enforce its disk cap", err)
			}
			image := filepath.Join(root, temporaryImageName)
			if err := engine.setupTemp(context.Background(), spec); err == nil {
				t.Fatal("repeated setup reformatted live temporary storage")
			}
			if err := os.Rename(image, image+".original"); err != nil {
				t.Fatal(err)
			}
			replacement, err := os.OpenFile(image, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_ = replacement.Truncate(size)
			_ = replacement.Close()
			if err := cleanupFinalizedTemp(spec); err == nil {
				t.Fatal("cleanup accepted a substituted backing image")
			}
			if err := os.Rename(image+".original", image); err != nil {
				t.Fatal(err)
			}
			if err := cleanupFinalizedTemp(spec); err != nil {
				t.Fatal("verified finalization could not unmount disk temporary storage", err)
			}
			if commandSucceeds(engine.config.MountpointPath, "--quiet", mount) {
				t.Fatal("temporary filesystem remains mounted")
			}
			if err := cleanupFinalizedTemp(spec); err != nil {
				t.Fatal("cleanup is not idempotent", err)
			}
		})
	}
}

func TestDiskTemporaryStorageFormatFailure(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_TEMP_STORAGE_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated root Linux temporary storage")
	}
	root := t.TempDir()
	spec := SessionSpec{SessionRoot: root, Policy: RuntimePolicy{TemporaryStorage: "disk", TemporarySizeBytes: 64 << 20}}
	engine := NewEngine(EngineConfig{MkfsExt4Path: "/bin/false"})
	if err := engine.setupTemp(context.Background(), spec); err == nil {
		t.Fatal("format failure was ignored")
	}
	if _, err := os.Lstat(filepath.Join(root, temporaryImageName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed launch left a backing image", err)
	}
	if err := os.WriteFile(filepath.Join(root, temporaryImageName), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.setupTemp(context.Background(), spec); err == nil {
		t.Fatal("preexisting image was overwritten")
	}
	data, err := os.ReadFile(filepath.Join(root, temporaryImageName))
	if err != nil || string(data) != "retained" {
		t.Fatal("failed retry changed retained image", err)
	}
}

func TestDiskTemporaryStorageLegacyTmpfsCleanup(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_TEMP_STORAGE_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated root Linux mount namespace")
	}
	spec := SessionSpec{SessionRoot: t.TempDir(), RuntimeUID: 65534, RuntimeGID: 65534,
		Policy: RuntimePolicy{TmpfsSizeBytes: 16 << 20}}
	engine := NewEngine(EngineConfig{})
	if err := engine.setupTemp(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.cleanupTemp(context.Background(), spec) })
	if err := cleanupFinalizedTemp(spec); err != nil {
		t.Fatal("old saved tmpfs session no longer cleans up", err)
	}
}
