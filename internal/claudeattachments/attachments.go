// Package claudeattachments grants Claude access to isolated session attachments.
package claudeattachments

import (
	"path/filepath"
	"slices"
)

// Directory returns the attachment directory as seen by the selected runtime.
func Directory(accountPath, sessionID string) string {
	return filepath.Join(accountPath, ".agent-remote-attachments", sessionID)
}

// Arguments preserves user arguments and grants only this session's attachment
// directory. The equals form cannot consume a positional prompt as another path.
func Arguments(argv []string, directory string) []string {
	index := slices.Index(argv, "--")
	if index < 0 {
		index = len(argv)
	}
	result := append([]string(nil), argv[:index]...)
	result = append(result, "--add-dir="+directory)
	return append(result, argv[index:]...)
}
