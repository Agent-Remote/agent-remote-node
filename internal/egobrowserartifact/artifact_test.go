package egobrowserartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyAcceptsPinnedImmutableRuntime(t *testing.T) {
	config, _ := prepareRuntime(t)
	if err := verify(config, false); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPinnedRejectsHistoricalWrapper(t *testing.T) {
	config, _ := prepareRuntime(t)
	if err := VerifyPinned(config); err == nil || !strings.Contains(err.Error(), "reviewed Node pin") {
		t.Fatalf("historical wrapper was not rejected by the Node pin: %v", err)
	}
}

func TestVerifyRejectsArtifactDrift(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(t *testing.T, releaseRoot string)
	}{
		{
			name: "wrapper bytes",
			tamper: func(t *testing.T, releaseRoot string) {
				t.Helper()
				path := filepath.Join(releaseRoot, "bin", "ego-browser")
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("changed"), 0o555); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Skill bytes",
			tamper: func(t *testing.T, releaseRoot string) {
				t.Helper()
				path := filepath.Join(releaseRoot, "skill", "ego-browser", "SKILL.md")
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("changed"), 0o444); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "source provenance",
			tamper: func(t *testing.T, releaseRoot string) {
				t.Helper()
				path := filepath.Join(releaseRoot, "ego-browser-skill-source.json")
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0o444); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "writable Skill file",
			tamper: func(t *testing.T, releaseRoot string) {
				t.Helper()
				path := filepath.Join(releaseRoot, "skill", "ego-browser", "SKILL.md")
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "Skill symlink",
			tamper: func(t *testing.T, releaseRoot string) {
				t.Helper()
				root := filepath.Join(releaseRoot, "skill", "ego-browser")
				if err := os.Symlink("SKILL.md", filepath.Join(root, "alias.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, releaseRoot := prepareRuntime(t)
			test.tamper(t, releaseRoot)
			if err := verify(config, false); err == nil {
				t.Fatal("drifted managed runtime was accepted")
			}
		})
	}
}

func TestVerifyRejectsWrapperAndSkillFromDifferentReleases(t *testing.T) {
	config, _ := prepareRuntime(t)
	otherConfig, _ := prepareRuntime(t)
	config.SkillPath = otherConfig.SkillPath
	if err := verify(config, false); err == nil || !strings.Contains(err.Error(), "same release") {
		t.Fatalf("unexpected validation result: %v", err)
	}
}

func TestPinnedSourceTreeDigest(t *testing.T) {
	source := filepath.Join("..", "managedskills", "skills", "ego-browser")
	digest, err := digestTree(source, false)
	if err != nil {
		t.Fatal(err)
	}
	if digest != OfficialSkillTreeSHA256 {
		t.Fatalf("official Skill digest = %s, want %s", digest, OfficialSkillTreeSHA256)
	}
	document, err := digestFile(filepath.Join(source, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if document != OfficialSkillDocumentSHA256 {
		t.Fatalf("official Skill document digest = %s, want %s", document, OfficialSkillDocumentSHA256)
	}
}

func prepareRuntime(t *testing.T) (RuntimeConfig, string) {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return os.Chmod(path, 0o644)
		})
	})
	releaseRoot := filepath.Join(root, "releases", "0.1.0")
	wrapperPath := filepath.Join(releaseRoot, "bin", "ego-browser")
	skillPath := filepath.Join(releaseRoot, "skill", "ego-browser")
	if err := os.MkdirAll(filepath.Dir(wrapperPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(wrapperPath, wrapper, 0o555); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join("..", "managedskills", "skills", "ego-browser"), skillPath)
	manifest, err := os.ReadFile(filepath.Join("..", "..", "ego-browser-skill-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(releaseRoot, "ego-browser-skill-source.json")
	if err := os.WriteFile(manifestPath, manifest, 0o444); err != nil {
		t.Fatal(err)
	}
	wrapperDigest := sha256.Sum256(wrapper)
	manifestDigest := sha256.Sum256(manifest)
	metadata := map[string]string{
		"VERSION":                "0.1.0",
		"WRAPPER_SHA256":         hex.EncodeToString(wrapperDigest[:]),
		"SKILL_VERSION":          OfficialSkillVersion,
		"SKILL_TREE_SHA256":      OfficialSkillTreeSHA256,
		"SOURCE_MANIFEST_SHA256": hex.EncodeToString(manifestDigest[:]),
	}
	for name, value := range metadata {
		if err := os.WriteFile(filepath.Join(releaseRoot, name), []byte(value+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(releaseRoot, "skill"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(releaseRoot, "bin"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(releaseRoot, 0o555); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(releaseRoot, current); err != nil {
		t.Fatal(err)
	}
	return RuntimeConfig{
		WrapperPath: filepath.Join(current, "bin", "ego-browser"), WrapperVersion: "0.1.0",
		SkillPath:    filepath.Join(current, "skill", "ego-browser"),
		SkillVersion: OfficialSkillVersion, SkillTreeSHA256: OfficialSkillTreeSHA256,
	}, releaseRoot
}

func copyTree(t *testing.T, source string, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o444)
	})
	if err != nil {
		t.Fatal(err)
	}
}
