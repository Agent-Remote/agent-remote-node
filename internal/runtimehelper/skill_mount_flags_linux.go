package runtimehelper

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Read the kernel mount table by mount ID, never by its escaped user-visible pathname.
// Recovery verifies these flags without remounting beneath an already running process.
func verifySkillMountFlags(parent *os.File) error {
	var target unix.Statx_t
	if err := unix.Statx(int(parent.Fd()), "skill-work", unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &target); err != nil {
		return err
	}
	if target.Mask&unix.STATX_MNT_ID == 0 {
		return errors.New("skill mount flags require a kernel mount identity")
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer file.Close()
	return verifySkillMountInfo(file, target.Mnt_id)
}

func verifySkillMountInfo(reader io.Reader, mountID uint64) error {
	const limit = 8 << 20
	bounded := &io.LimitedReader{R: reader, N: limit + 1}
	scanner := bufio.NewScanner(bounded)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	expected := strconv.FormatUint(mountID, 10)
	found := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != expected {
			continue
		}
		// A private bind has no propagation fields between mount options and the separator.
		if found || len(fields) != 10 || fields[6] != "-" {
			return errors.New("skill work mount is not uniquely private")
		}
		options := "," + fields[5] + ","
		for _, flag := range []string{"rw", "nosuid", "nodev"} {
			if !strings.Contains(options, ","+flag+",") {
				return errors.New("skill work mount lost required security flags")
			}
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !found || bounded.N == 0 {
		return errors.New("skill work mount flags could not be verified")
	}
	return nil
}
