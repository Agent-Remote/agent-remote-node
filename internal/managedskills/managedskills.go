// Package managedskills installs Agent Remote-owned skills into tool accounts.
package managedskills

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	deviceSkillDirectory           = ".claude/skills/agent-remote-device"
	deviceSkillReferencesDirectory = deviceSkillDirectory + "/references"
	deviceSkillPath                = deviceSkillDirectory + "/SKILL.md"
	deviceBrowserReferencePath     = deviceSkillReferencesDirectory + "/browser.md"
	egoBrowserSkillDirectory       = ".claude/skills/ego-browser"
	egoBrowserEmbeddedRoot         = "skills/ego-browser"
	// EgoBrowserSkillVersion is the exact reviewed upstream Skill release.
	EgoBrowserSkillVersion = "1.2.3"
	// EgoBrowserSkillSourceCommit pins the upstream tree used by this Node release.
	EgoBrowserSkillSourceCommit = "36053d07001a910cb806a15d42d00fdea1cdea3d"
	// EgoBrowserSkillDocumentSHA256 pins the upstream SKILL.md bytes.
	EgoBrowserSkillDocumentSHA256 = "44c119634df847861486c3b104cda3c2faa9dd4c71dbb3a854429098be962293"
	// EgoBrowserSkillTreeSHA256 pins every path and byte in the managed Skill tree.
	EgoBrowserSkillTreeSHA256 = "262110a09678fd3e0bbb382400588dacb98b24659b3b4a57903703b65d133c7c"
)

//go:embed skills/agent-remote-device/SKILL.md
var deviceSkill []byte

//go:embed skills/agent-remote-device/references/browser.md
var deviceBrowserReference []byte

//go:embed skills/ego-browser
var egoBrowserSkill embed.FS

type managedFile struct {
	path    string
	content []byte
}

// Ownership identifies the account user that should own installed resources.
type Ownership struct {
	UID int
	GID int
}

// InstallClaude installs or updates Agent Remote-owned Claude skills without
// changing any other account configuration.
func InstallClaude(accountPath string, ownership *Ownership) error {
	egoBrowserFiles, err := verifiedEgoBrowserSkillFiles()
	if err != nil {
		return fmt.Errorf("verify official ego-browser Skill %s: %w", EgoBrowserSkillVersion, err)
	}
	root, err := os.OpenRoot(accountPath)
	if err != nil {
		return fmt.Errorf("open Claude account root: %w", err)
	}
	defer root.Close()

	for _, path := range []string{
		".claude/skills",
		deviceSkillDirectory,
		deviceSkillReferencesDirectory,
		egoBrowserSkillDirectory,
	} {
		if err := ensureDirectory(root, path, ownership); err != nil {
			return fmt.Errorf("prepare managed skill directory %s: %w", path, err)
		}
	}
	for _, file := range []managedFile{
		{path: deviceSkillPath, content: deviceSkill},
		{path: deviceBrowserReferencePath, content: deviceBrowserReference},
	} {
		if err := installManagedFile(root, file, ownership); err != nil {
			return fmt.Errorf("install managed skill file %s: %w", file.path, err)
		}
	}
	for _, file := range egoBrowserFiles {
		if err := ensureDirectory(root, filepath.Dir(file.path), ownership); err != nil {
			return fmt.Errorf("prepare official ego-browser Skill directory: %w", err)
		}
		if err := installManagedFile(root, file, ownership); err != nil {
			return fmt.Errorf("install official ego-browser Skill file %s: %w", file.path, err)
		}
	}
	return nil
}

func verifiedEgoBrowserSkillFiles() ([]managedFile, error) {
	files := make([]managedFile, 0, 16)
	treeDigest := sha256.New()
	err := fs.WalkDir(egoBrowserSkill, egoBrowserEmbeddedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("embedded path %s is not a regular file", path)
		}
		content, err := egoBrowserSkill.ReadFile(path)
		if err != nil {
			return err
		}
		relative, found := strings.CutPrefix(path, egoBrowserEmbeddedRoot+"/")
		if !found || relative == "" || !fs.ValidPath(relative) {
			return fmt.Errorf("embedded path %s is invalid", path)
		}
		treeDigest.Write([]byte(relative))
		treeDigest.Write([]byte{0})
		treeDigest.Write([]byte(strconv.Itoa(len(content))))
		treeDigest.Write([]byte{0})
		treeDigest.Write(content)
		treeDigest.Write([]byte{0})
		files = append(files, managedFile{
			path:    filepath.Join(egoBrowserSkillDirectory, filepath.FromSlash(relative)),
			content: content,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("official Skill tree is empty")
	}
	document := files[0]
	if filepath.ToSlash(document.path) != egoBrowserSkillDirectory+"/SKILL.md" {
		return nil, errors.New("official Skill document is missing")
	}
	documentDigest := sha256.Sum256(document.content)
	if hex.EncodeToString(documentDigest[:]) != EgoBrowserSkillDocumentSHA256 {
		return nil, errors.New("official Skill document digest mismatch")
	}
	if hex.EncodeToString(treeDigest.Sum(nil)) != EgoBrowserSkillTreeSHA256 {
		return nil, errors.New("official Skill tree digest mismatch")
	}
	return files, nil
}

func installManagedFile(root *os.Root, file managedFile, ownership *Ownership) error {
	info, err := root.Lstat(file.path)
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("managed skill path is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect managed skill: %w", err)
	}
	current, err := root.ReadFile(file.path)
	if err == nil && bytes.Equal(current, file.content) {
		return applyFileMetadata(root, file.path, ownership)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read managed skill: %w", err)
	}
	if err := writeFileAtomic(root, file.path, file.content, ownership); err != nil {
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
