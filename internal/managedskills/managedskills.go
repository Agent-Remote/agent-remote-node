// Package managedskills installs Agent Remote-owned skills into tool accounts.
package managedskills

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	deviceSkillDirectory = ".claude/skills/agent-remote-device"
	deviceSkillPath      = deviceSkillDirectory + "/SKILL.md"
)

//go:embed skills/agent-remote-device/SKILL.md
var deviceSkill []byte

// Ownership identifies the account user that should own installed resources.
type Ownership struct {
	UID int
	GID int
}

// InstallClaude installs or updates Agent Remote-owned Claude skills without
// changing any other account configuration.
func InstallClaude(accountPath string, ownership *Ownership) error {
	root, err := os.OpenRoot(accountPath)
	if err != nil {
		return fmt.Errorf("open Claude account root: %w", err)
	}
	defer root.Close()

	for _, path := range []string{".claude/skills", deviceSkillDirectory} {
		if err := ensureDirectory(root, path, ownership); err != nil {
			return fmt.Errorf("prepare managed skill directory %s: %w", path, err)
		}
	}
	info, err := root.Lstat(deviceSkillPath)
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("managed skill path is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect managed skill: %w", err)
	}
	current, err := root.ReadFile(deviceSkillPath)
	if err == nil && bytes.Equal(current, deviceSkill) {
		return applyFileMetadata(root, deviceSkillPath, ownership)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read managed skill: %w", err)
	}
	if err := writeFileAtomic(root, deviceSkillPath, deviceSkill, ownership); err != nil {
		return fmt.Errorf("write managed skill: %w", err)
	}
	return nil
}

func ensureDirectory(root *os.Root, path string, ownership *Ownership) error {
	if err := root.MkdirAll(path, 0o700); err != nil {
		return err
	}
	directory, err := root.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("path is not a directory")
	}
	if ownership != nil {
		return directory.Chown(ownership.UID, ownership.GID)
	}
	return nil
}

func writeFileAtomic(root *os.Root, path string, content []byte, ownership *Ownership) error {
	temporaryPath, temporary, err := createTemporaryFile(root, filepath.Dir(path))
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = root.Remove(temporaryPath)
	}()
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if ownership != nil {
		if err := temporary.Chown(ownership.UID, ownership.GID); err != nil {
			return err
		}
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := root.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func createTemporaryFile(root *os.Root, directory string) (string, *os.File, error) {
	for range 10 {
		var suffix [12]byte
		if _, err := io.ReadFull(rand.Reader, suffix[:]); err != nil {
			return "", nil, err
		}
		path := filepath.Join(directory, fmt.Sprintf(".agent-remote-skill-%x", suffix))
		file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return path, file, err
	}
	return "", nil, errors.New("could not allocate temporary skill file")
}

func applyFileMetadata(root *os.Root, path string, ownership *Ownership) error {
	file, err := root.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("managed skill is not a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if ownership != nil {
		return file.Chown(ownership.UID, ownership.GID)
	}
	return nil
}
