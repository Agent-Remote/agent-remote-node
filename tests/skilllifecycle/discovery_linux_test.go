package skilllifecycle

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func newClaudeRunID(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal("cannot generate Claude session identity")
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}

func assertClaudeSkillDiscovery(t *testing.T, f lifecycleFixture, runID string) {
	t.Helper()
	account := filepath.Join(usersRoot, f.UserID, "tool-accounts/claude", f.AccountID, ".claude")
	if err := inspectClaudeDiscovery(account, runID); err != nil {
		t.Fatal("real Claude session did not provide valid successful Skill discovery evidence")
	}
}

func inspectClaudeDiscovery(account, runID string) error {
	invalid := errors.New("Claude discovery transcript unavailable or unsafe")
	root, err := os.OpenRoot(account)
	if err != nil {
		return invalid
	}
	defer root.Close()
	match := ""
	count := 0
	// Root confines reads even if an unexpected concurrent writer changes an ancestor.
	err = fs.WalkDir(root.FS(), "projects", func(path string, entry fs.DirEntry, walkErr error) error {
		count++
		if walkErr != nil || count > claudeTraceEntries || strings.Count(path, "/") > 8 || entry.Type()&os.ModeSymlink != 0 {
			return invalid
		}
		if entry.Name() != runID+".jsonl" {
			return nil
		}
		if !entry.Type().IsRegular() || match != "" {
			return invalid
		}
		match = path
		return nil
	})
	if err != nil || match == "" {
		return invalid
	}
	before, err := root.Lstat(match)
	if err != nil || !before.Mode().IsRegular() || before.Size() > claudeTraceBytes {
		return invalid
	}
	file, err := root.OpenFile(match, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return invalid
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return invalid
	}
	if err := verifyClaudeDiscovery(file, runID); err != nil {
		return invalid
	}
	after, err := root.Lstat(match)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return invalid
	}
	return nil
}
