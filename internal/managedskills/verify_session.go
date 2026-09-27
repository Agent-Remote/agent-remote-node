package managedskills

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// VerifySessionSkills checks the exact readonly system trees selected for a managed session.
// It never repairs or updates an existing tree, including one already mounted into a runtime.
func VerifySessionSkills(path string, includeDevice bool) error {
	egoFiles, err := verifiedEgoBrowserSkillFiles()
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || verifySessionEntry(info, true) != nil {
		return errors.New("invalid prepared system skill root")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, parent := range []string{".claude", ".claude/skills"} {
		info, err := root.Lstat(parent)
		if err != nil || verifySessionEntry(info, true) != nil {
			return errors.New("invalid prepared system skill ancestor")
		}
	}
	if err := verifySessionTree(root, egoBrowserSkillDirectory, egoFiles); err != nil {
		return err
	}
	if includeDevice {
		return verifySessionTree(root, deviceSkillDirectory, []managedFile{
			{path: deviceSkillPath, content: deviceSkill}, {path: deviceBrowserReferencePath, content: deviceBrowserReference},
		})
	}
	return nil
}

func verifySessionTree(root *os.Root, base string, files []managedFile) error {
	expectedFiles := make(map[string][]byte, len(files))
	directories := map[string]bool{base: true}
	for _, file := range files {
		expectedFiles[filepath.ToSlash(file.path)] = file.content
		for parent := filepath.ToSlash(filepath.Dir(file.path)); parent != base; parent = filepath.ToSlash(filepath.Dir(parent)) {
			directories[parent] = true
		}
	}
	count := 0
	err := fs.WalkDir(root.FS(), base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil || verifySessionEntry(info, entry.IsDir()) != nil {
			return errors.New("unsafe prepared system skill entry")
		}
		if entry.IsDir() {
			if !directories[path] {
				return errors.New("unexpected prepared system skill directory")
			}
			return nil
		}
		content, known := expectedFiles[path]
		if !known || info.Size() != int64(len(content)) {
			return errors.New("unexpected prepared system skill file")
		}
		file, err := root.Open(path)
		if err != nil {
			return err
		}
		actual, readErr := io.ReadAll(io.LimitReader(file, int64(len(content))+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(actual, content) {
			return errors.New("prepared system skill bytes differ from the pinned source")
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(expectedFiles) {
		return errors.New("prepared system skill tree is incomplete")
	}
	return nil
}

func verifySessionEntry(info os.FileInfo, directory bool) error {
	if info == nil || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid prepared system skill entry type")
	}
	expectedMode := os.FileMode(0o444)
	if directory {
		expectedMode = 0o555
	}
	if info.Mode().Perm() != expectedMode || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("invalid prepared system skill permissions")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || runtime.GOOS == "linux" && stat.Uid != 0 || !directory && stat.Nlink != 1 {
		return errors.New("invalid prepared system skill ownership or hard link")
	}
	return nil
}
