package managedskills

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func sessionSkillTree(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("requires Helper-owned Linux system files")
	}
	root := t.TempDir()
	if err := InstallClaude(root, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if entry.IsDir() {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestVerifySessionSkillsRejectsChangedOrAdditionalEntries(t *testing.T) {
	for _, kind := range []string{"bytes", "missing", "extra_file", "extra_directory", "symlink", "hardlink", "file_mode", "directory_mode"} {
		t.Run(kind, func(t *testing.T) {
			root := sessionSkillTree(t)
			if err := VerifySessionSkills(root, true); err != nil {
				t.Fatal("valid readonly copy rejected", err)
			}
			directory := filepath.Join(root, egoBrowserSkillDirectory)
			path := filepath.Join(directory, "SKILL.md")
			if err := os.Chmod(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "bytes":
				var data []byte
				data, err = os.ReadFile(path)
				if err == nil {
					data[0] ^= 1
					err = os.Chmod(path, 0o644)
				}
				if err == nil {
					err = os.WriteFile(path, data, 0o444)
				}
				if err == nil {
					err = os.Chmod(path, 0o444)
				}
			case "missing":
				err = os.Remove(path)
			case "extra_file":
				err = os.WriteFile(filepath.Join(directory, "extra.md"), []byte("unexpected"), 0o444)
			case "extra_directory":
				err = os.Mkdir(filepath.Join(directory, "unexpected"), 0o555)
			case "symlink":
				err = os.Remove(path)
				if err == nil {
					err = os.Symlink("references/api.md", path)
				}
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "linked-document"))
			case "file_mode":
				err = os.Chmod(path, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind != "directory_mode" {
				if err := os.Chmod(directory, 0o555); err != nil {
					t.Fatal(err)
				}
			}
			if err := VerifySessionSkills(root, true); err == nil {
				t.Fatal("changed system tree was accepted")
			}
		})
	}
}

func TestVerifySessionSkillsChecksOnlySelectedDeviceTree(t *testing.T) {
	root := sessionSkillTree(t)
	directory := filepath.Join(root, deviceSkillDirectory)
	if err := os.Chmod(filepath.Dir(directory), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			return os.Chmod(path, 0o755)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(directory), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := VerifySessionSkills(root, false); err != nil {
		t.Fatal("unselected device tree became a runtime dependency", err)
	}
	if err := VerifySessionSkills(root, true); err == nil {
		t.Fatal("missing selected device skill was accepted")
	}
}
