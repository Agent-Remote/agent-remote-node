package managedskills

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInstallClaudeCreatesAndUpdatesManagedSkill(t *testing.T) {
	accountPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(accountPath, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	ownership := &Ownership{UID: os.Getuid(), GID: os.Getgid()}
	if err := InstallClaude(accountPath, ownership); err != nil {
		t.Fatal(err)
	}
	assertInstalledSkillMatchesSource(t, accountPath)
	managedFiles := []managedFile{
		{path: deviceSkillPath, content: deviceSkill},
		{path: deviceBrowserReferencePath, content: deviceBrowserReference},
	}
	for _, file := range managedFiles {
		target := filepath.Join(accountPath, filepath.FromSlash(file.path))
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(file.content) {
			t.Fatalf("installed file %s does not match embedded content", file.path)
		}
		if err := os.WriteFile(target, []byte("stale\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(accountPath, ".claude", "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(unrelated), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(accountPath, ownership); err != nil {
		t.Fatal(err)
	}
	custom, err := os.ReadFile(unrelated)
	if err != nil || string(custom) != "custom\n" {
		t.Fatalf("unrelated skill changed: content=%q err=%v", custom, err)
	}
	for _, file := range managedFiles {
		target := filepath.Join(accountPath, filepath.FromSlash(file.path))
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(file.content) {
			t.Fatalf("stale managed file %s was not updated", file.path)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("managed file %s mode = %o, want 600", file.path, info.Mode().Perm())
		}
	}
}

func assertInstalledSkillMatchesSource(t *testing.T, accountPath string) {
	t.Helper()
	sourceRoot := filepath.Join("skills", "agent-remote-device")
	installedRoot := filepath.Join(accountPath, filepath.FromSlash(deviceSkillDirectory))
	sourceFiles := readSkillTree(t, sourceRoot)
	installedFiles := readSkillTree(t, installedRoot)
	if !reflect.DeepEqual(installedFiles, sourceFiles) {
		t.Fatalf("installed skill differs from node source: installed=%#v source=%#v", installedFiles, sourceFiles)
	}
}

func readSkillTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestInstallClaudeRejectsSkillPathEscapingAccount(t *testing.T) {
	accountPath := t.TempDir()
	outsidePath := t.TempDir()
	if err := os.Mkdir(filepath.Join(accountPath, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(accountPath, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(accountPath, nil); err == nil {
		t.Fatal("expected an escaping skills symlink to be rejected")
	}
	entries, err := os.ReadDir(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("installer wrote outside account root: %#v", entries)
	}
}

func TestInstallClaudeRejectsManagedFileSymlink(t *testing.T) {
	accountPath := t.TempDir()
	settingsPath := filepath.Join(accountPath, ".claude", "settings.json")
	target := filepath.Join(accountPath, filepath.FromSlash(deviceSkillPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, deviceSkill, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../settings.json", target); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(accountPath, &Ownership{UID: os.Getuid(), GID: os.Getgid()}); err == nil {
		t.Fatal("expected a managed file symlink to be rejected")
	}
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("symlink target mode changed to %o", info.Mode().Perm())
	}
}
