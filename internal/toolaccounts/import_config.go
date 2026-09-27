package toolaccounts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSkillManagerOwnsPath rejects the entire batch before any account file changes.
var ErrSkillManagerOwnsPath = errors.New("SKILL_MANAGER_OWNS_PATH")

const (
	maxImportFileBytes  = 1 << 20
	maxImportTotalBytes = 8 << 20
)

// ImportOwnership is chosen by the privileged helper, never by a task payload.
type ImportOwnership struct{ UID, GID int }

type preparedImportFile struct {
	path    string
	content []byte
	mode    os.FileMode
}

// ImportConfig validates the whole batch against freshly authorized ownership before writing.
func ImportConfig(root string, payload ImportConfigPayload, directoryMode string) (ImportConfigResult, error) {
	return ImportConfigAs(root, payload, directoryMode, nil)
}

// ImportConfigAs writes using an optional helper-selected non-root runtime identity.
func ImportConfigAs(root string, payload ImportConfigPayload, directoryMode string, owner *ImportOwnership) (ImportConfigResult, error) {
	if owner != nil && (owner.UID <= 0 || owner.GID <= 0) {
		return ImportConfigResult{}, errors.New("invalid config import ownership")
	}
	if directoryMode != "legacy" && directoryMode != "migrating" && directoryMode != "managed_v1" {
		return ImportConfigResult{}, errors.New("unknown account skill directory mode")
	}
	accountPath, err := resolveAccountPath(root, payload.UserID, payload.ToolType, payload.ToolAccountID, payload.AccountRemotePath)
	if err != nil {
		return ImportConfigResult{}, err
	}
	files, err := prepareImportFiles(accountPath, payload.Files, directoryMode)
	if err != nil {
		return ImportConfigResult{}, err
	}
	if err := writeImportBatch(root, files, owner); err != nil {
		return ImportConfigResult{}, err
	}
	filesWritten := make([]string, 0, len(files))
	for _, file := range payload.Files {
		filesWritten = append(filesWritten, file.Path)
	}
	return ImportConfigResult{
		Status: "imported", ToolAccountID: payload.ToolAccountID, ToolType: payload.ToolType,
		AccountRemotePath: accountPath, FilesWritten: filesWritten,
	}, nil
}

func prepareImportFiles(accountPath string, files []ImportConfigFile, directoryMode string) ([]preparedImportFile, error) {
	if len(files) == 0 {
		return nil, errors.New("config import files are required")
	}
	encoded, err := json.Marshal(files)
	if err != nil || len(encoded) > 12<<20 {
		return nil, errors.New("config import file list exceeds encoded size limit")
	}
	prepared := make([]preparedImportFile, 0, len(files))
	total := 0
	targets := make(map[string]struct{}, len(files))
	for _, file := range files {
		target, err := resolveImportConfigTarget(accountPath, file.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := targets[target]; exists {
			return nil, errors.New("duplicate config import target")
		}
		targets[target] = struct{}{}
		if directoryMode != "legacy" && (file.Path == "~/.claude/skills" || strings.HasPrefix(file.Path, "~/.claude/skills/")) {
			return nil, ErrSkillManagerOwnsPath
		}
		if len(file.ContentBase64) > base64.StdEncoding.EncodedLen(maxImportFileBytes) {
			return nil, errors.New("config import file exceeds size limit")
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("decode config import file: %w", err)
		}
		total += len(content)
		if len(content) > maxImportFileBytes || total > maxImportTotalBytes {
			return nil, errors.New("config import content exceeds size limit")
		}
		prepared = append(prepared, preparedImportFile{path: target, content: content, mode: sanitizeFileMode(file.Mode)})
	}
	for target := range targets {
		for parent := filepath.Dir(target); parent != accountPath && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if _, exists := targets[parent]; exists {
				return nil, errors.New("config import file conflicts with a directory target")
			}
		}
	}
	return prepared, nil
}

// ValidateImportConfig checks immutable input and ownership without filesystem mutation.
func ValidateImportConfig(root string, payload ImportConfigPayload, mode string) error {
	if mode != "legacy" && mode != "migrating" && mode != "managed_v1" {
		return errors.New("invalid config import directory mode")
	}
	account, err := resolveAccountPath(root, payload.UserID, payload.ToolType, payload.ToolAccountID, payload.AccountRemotePath)
	if err != nil {
		return err
	}
	_, err = prepareImportFiles(account, payload.Files, mode)
	return err
}
