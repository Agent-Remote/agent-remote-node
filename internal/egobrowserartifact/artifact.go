// Package egobrowserartifact verifies the immutable wrapper and official Skill release.
package egobrowserartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const (
	// OfficialSkillVersion is the reviewed upstream ego-browser Skill release.
	OfficialSkillVersion = "1.2.3"
	// OfficialSkillSourceCommit pins the reviewed upstream ego-lite tree.
	OfficialSkillSourceCommit = "36053d07001a910cb806a15d42d00fdea1cdea3d"
	// OfficialSkillDocumentSHA256 pins the reviewed upstream SKILL.md bytes.
	OfficialSkillDocumentSHA256 = "44c119634df847861486c3b104cda3c2faa9dd4c71dbb3a854429098be962293"
	// OfficialSkillTreeSHA256 pins every path and byte in the official Skill tree.
	OfficialSkillTreeSHA256 = "262110a09678fd3e0bbb382400588dacb98b24659b3b4a57903703b65d133c7c"
)

// RuntimeConfig identifies one expected immutable ego-browser release.
type RuntimeConfig struct {
	WrapperPath     string
	WrapperVersion  string
	SkillPath       string
	SkillVersion    string
	SkillTreeSHA256 string
}

type sourceManifest struct {
	SchemaVersion       int    `json:"schema_version"`
	Name                string `json:"name"`
	Version             string `json:"version"`
	UpstreamRepository  string `json:"upstream_repository"`
	UpstreamTag         string `json:"upstream_tag"`
	UpstreamCommit      string `json:"upstream_commit"`
	SkillDocumentSHA256 string `json:"skill_document_sha256"`
	TreeSHA256          string `json:"tree_sha256"`
}

// Verify checks release metadata, provenance, permissions, and current bytes.
func Verify(config RuntimeConfig) error {
	return verify(config, runtime.GOOS == "linux")
}

func verify(config RuntimeConfig, requireRootOwner bool) error {
	if !safeAbsolutePath(config.WrapperPath) || !safeAbsolutePath(config.SkillPath) {
		return errors.New("ego-browser managed artifact path is invalid")
	}
	if !validVersion(config.WrapperVersion) || !validVersion(config.SkillVersion) {
		return errors.New("ego-browser managed artifact version is invalid")
	}
	if !validSHA256(config.SkillTreeSHA256) {
		return errors.New("ego-browser Skill tree digest is invalid")
	}

	resolvedWrapper, err := filepath.EvalSymlinks(config.WrapperPath)
	if err != nil {
		return errors.New("ego-browser wrapper is unavailable")
	}
	resolvedSkill, err := filepath.EvalSymlinks(config.SkillPath)
	if err != nil {
		return errors.New("ego-browser Skill is unavailable")
	}
	if filepath.Base(resolvedWrapper) != "ego-browser" || filepath.Base(filepath.Dir(resolvedWrapper)) != "bin" ||
		filepath.Base(resolvedSkill) != "ego-browser" || filepath.Base(filepath.Dir(resolvedSkill)) != "skill" {
		return errors.New("ego-browser managed artifact layout is invalid")
	}
	wrapperRelease := filepath.Dir(filepath.Dir(resolvedWrapper))
	skillRelease := filepath.Dir(filepath.Dir(resolvedSkill))
	if wrapperRelease != skillRelease || filepath.Base(wrapperRelease) != config.WrapperVersion {
		return errors.New("ego-browser wrapper and Skill are not from the same release")
	}
	if err := validateEntry(wrapperRelease, true, false, requireRootOwner); err != nil {
		return fmt.Errorf("validate ego-browser release root: %w", err)
	}
	if err := validateEntry(resolvedWrapper, false, true, requireRootOwner); err != nil {
		return fmt.Errorf("validate ego-browser wrapper: %w", err)
	}
	if err := validateEntry(resolvedSkill, true, false, requireRootOwner); err != nil {
		return fmt.Errorf("validate ego-browser Skill root: %w", err)
	}

	version, err := readMetadata(wrapperRelease, "VERSION", requireRootOwner)
	if err != nil || version != config.WrapperVersion {
		return errors.New("ego-browser wrapper version metadata mismatch")
	}
	skillVersion, err := readMetadata(wrapperRelease, "SKILL_VERSION", requireRootOwner)
	if err != nil || skillVersion != config.SkillVersion {
		return errors.New("ego-browser Skill version metadata mismatch")
	}
	wrapperDigest, err := readMetadata(wrapperRelease, "WRAPPER_SHA256", requireRootOwner)
	if err != nil || !validSHA256(wrapperDigest) {
		return errors.New("ego-browser wrapper digest metadata is invalid")
	}
	actualWrapperDigest, err := digestFile(resolvedWrapper)
	if err != nil || !strings.EqualFold(wrapperDigest, actualWrapperDigest) {
		return errors.New("ego-browser wrapper digest mismatch")
	}
	skillDigest, err := readMetadata(wrapperRelease, "SKILL_TREE_SHA256", requireRootOwner)
	if err != nil || !strings.EqualFold(skillDigest, config.SkillTreeSHA256) {
		return errors.New("ego-browser Skill digest metadata mismatch")
	}
	actualSkillDigest, err := digestTree(resolvedSkill, requireRootOwner)
	if err != nil || !strings.EqualFold(skillDigest, actualSkillDigest) {
		return errors.New("ego-browser Skill tree digest mismatch")
	}
	documentDigest, err := digestFile(filepath.Join(resolvedSkill, "SKILL.md"))
	if err != nil || documentDigest != OfficialSkillDocumentSHA256 {
		return errors.New("ego-browser Skill document digest mismatch")
	}
	if err := verifySourceManifest(wrapperRelease, config, requireRootOwner); err != nil {
		return err
	}
	return nil
}

func verifySourceManifest(releaseRoot string, config RuntimeConfig, requireRootOwner bool) error {
	manifestPath := filepath.Join(releaseRoot, "ego-browser-skill-source.json")
	if err := validateEntry(manifestPath, false, false, requireRootOwner); err != nil {
		return errors.New("ego-browser Skill source manifest is invalid")
	}
	manifestDigest, err := readMetadata(releaseRoot, "SOURCE_MANIFEST_SHA256", requireRootOwner)
	if err != nil || !validSHA256(manifestDigest) {
		return errors.New("ego-browser Skill source manifest digest metadata is invalid")
	}
	actualDigest, err := digestFile(manifestPath)
	if err != nil || !strings.EqualFold(manifestDigest, actualDigest) {
		return errors.New("ego-browser Skill source manifest digest mismatch")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil || len(data) > 16<<10 {
		return errors.New("ego-browser Skill source manifest is unreadable")
	}
	var manifest sourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return errors.New("ego-browser Skill source manifest JSON is invalid")
	}
	if manifest.SchemaVersion != 1 || manifest.Name != "ego-browser" ||
		manifest.Version != config.SkillVersion || manifest.Version != OfficialSkillVersion ||
		manifest.UpstreamRepository != "https://github.com/citrolabs/ego-lite" ||
		manifest.UpstreamTag != "v"+OfficialSkillVersion ||
		manifest.UpstreamCommit != OfficialSkillSourceCommit ||
		manifest.SkillDocumentSHA256 != OfficialSkillDocumentSHA256 ||
		manifest.TreeSHA256 != config.SkillTreeSHA256 || manifest.TreeSHA256 != OfficialSkillTreeSHA256 {
		return errors.New("ego-browser Skill source provenance mismatch")
	}
	return nil
}

func readMetadata(releaseRoot string, name string, requireRootOwner bool) (string, error) {
	path := filepath.Join(releaseRoot, name)
	if err := validateEntry(path, false, false, requireRootOwner); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 512 {
		return "", errors.New("metadata is unreadable")
	}
	return strings.TrimSpace(string(data)), nil
}

func validateEntry(path string, directory bool, executable bool, requireRootOwner bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if directory != info.IsDir() || (!directory && !info.Mode().IsRegular()) {
		return errors.New("entry has an unexpected type")
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("entry is replaceable by an untrusted runtime")
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return errors.New("entry is not executable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("entry ownership is unavailable")
	}
	if requireRootOwner && stat.Uid != 0 {
		return errors.New("entry is not root-owned")
	}
	if !directory && stat.Nlink != 1 {
		return errors.New("entry has multiple hard links")
	}
	return nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func digestTree(root string, requireRootOwner bool) (string, error) {
	digest := sha256.New()
	fileCount := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return validateEntry(path, true, false, requireRootOwner)
		}
		if !entry.Type().IsRegular() {
			return errors.New("Skill tree contains a symlink or non-regular entry")
		}
		if err := validateEntry(path, false, false, requireRootOwner); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			return errors.New("Skill tree path is invalid")
		}
		writeTreeField(digest, filepath.ToSlash(relative))
		writeTreeField(digest, strconv.FormatInt(info.Size(), 10))
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		_, _ = digest.Write([]byte{0})
		fileCount++
		return nil
	})
	if err != nil {
		return "", err
	}
	if fileCount == 0 {
		return "", errors.New("Skill tree is empty")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeTreeField(digest hash.Hash, value string) {
	_, _ = digest.Write([]byte(value))
	_, _ = digest.Write([]byte{0})
}

func safeAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.Contains(path, "..")
}

func validVersion(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || (index > 0 && strings.ContainsRune("._+-", character)) {
			continue
		}
		return false
	}
	return true
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}
