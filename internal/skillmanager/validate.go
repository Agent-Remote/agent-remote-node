package skillmanager

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var contentDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var dependencyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// Validate checks a complete canonical tree and never follows host filesystem links.
func Validate(manifest Manifest) error {
	if manifest.Version != 1 || len(manifest.Entries) > 100_000 {
		return errors.New("unsupported skill manifest version or entry count")
	}
	byPath := make(map[string]Entry, len(manifest.Entries))
	previous := ""
	var total int64
	for _, entry := range manifest.Entries {
		if err := validateEntry(entry); err != nil {
			return err
		}
		if entry.Path <= previous {
			return errors.New("manifest paths must be unique and UTF-8 sorted")
		}
		previous = entry.Path
		parts := strings.Split(entry.Path, "/")
		for index := 1; index < len(parts); index++ {
			parent, exists := byPath[strings.Join(parts[:index], "/")]
			if !exists || parent.Kind != "directory" {
				return errors.New("manifest requires explicit directory parents")
			}
		}
		if total > math.MaxInt64-entry.Size {
			return errors.New("skill tree byte count overflows the protocol")
		}
		total += entry.Size
		byPath[entry.Path] = entry
	}
	for _, entry := range manifest.Entries {
		if entry.Kind == "symlink" {
			if err := validateLink(entry, byPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidatePath checks the portable, NFC-normalized relative POSIX path contract.
func ValidatePath(value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return errors.New("skill path must be relative POSIX text")
	}
	if !validPathText(value) {
		return errors.New("skill path is not canonical UTF-8 text")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return errors.New("skill path contains an invalid component")
		}
	}
	return nil
}

func validPathText(value string) bool {
	if !utf8.ValidString(value) || len(value) > 4096 || !norm.NFC.IsNormalString(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validateEntry(entry Entry) error {
	if err := ValidatePath(entry.Path); err != nil {
		return err
	}
	if entry.Mode > 0o777 || entry.Size < 0 {
		return errors.New("skill entry has invalid size or mode")
	}
	if entry.Kind == "file" {
		if !contentDigestPattern.MatchString(entry.SHA256) ||
			entry.ContentKind != "text" && entry.ContentKind != "binary" ||
			entry.Target != "" || entry.Dependency != "" {
			return errors.New("file entry has inconsistent metadata")
		}
		return nil
	}
	if entry.Size != 0 || entry.SHA256 != "" || entry.ContentKind != "" {
		return errors.New("non-file entry cannot contain file metadata")
	}
	if entry.Kind == "directory" {
		if entry.Target != "" || entry.Dependency != "" {
			return errors.New("directory cannot contain a link target")
		}
		return nil
	}
	if entry.Kind != "symlink" && entry.Kind != "runtime_link" {
		return errors.New("unsupported skill entry kind")
	}
	if entry.Mode != 0o777 || entry.Target == "" || strings.Contains(entry.Target, "\\") || !validPathText(entry.Target) {
		return errors.New("link entry has invalid target or mode")
	}
	if entry.Kind == "runtime_link" {
		if !strings.HasPrefix(entry.Target, "/") || !dependencyPattern.MatchString(entry.Dependency) {
			return errors.New("runtime link requires an absolute target and dependency")
		}
		return ValidatePath(strings.TrimPrefix(entry.Target, "/"))
	}
	if strings.HasPrefix(entry.Target, "/") || entry.Dependency != "" {
		return errors.New("ordinary link must stay relative and cannot claim a dependency")
	}
	return nil
}

func validateLink(entry Entry, entries map[string]Entry) error {
	return validateLinkLookup(entry, func(path string) (Entry, error) {
		target, exists := entries[path]
		if !exists {
			return Entry{}, errors.New("link target is missing from manifest")
		}
		return target, nil
	})
}

func validateLinkLookup(entry Entry, lookup func(string) (Entry, error)) error {
	parts := strings.Split(entry.Path, "/")
	resolved := append([]string{}, parts[:len(parts)-1]...)
	pending := strings.Split(entry.Target, "/")
	hops := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(resolved) == 0 {
				return errors.New("link escapes manifest root")
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		path := strings.Join(append(append([]string{}, resolved...), part), "/")
		target, err := lookup(path)
		if err != nil {
			return err
		}
		if target.Kind == "symlink" {
			hops++
			if hops > 40 {
				return errors.New("link resolution exceeds the cycle limit")
			}
			pending = append(strings.Split(target.Target, "/"), pending...)
			continue
		}
		if len(pending) > 0 && target.Kind != "directory" {
			return errors.New("link traverses a non-directory entry")
		}
		resolved = append(resolved, part)
	}
	if len(resolved) == 0 || strings.HasPrefix(entry.Path, strings.Join(resolved, "/")+"/") {
		return errors.New("link to the manifest root or ancestor creates a cycle")
	}
	return nil
}
