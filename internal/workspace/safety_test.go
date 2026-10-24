package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareNeverWritesThroughWorkspaceAliases(t *testing.T) {
	for _, alias := range []string{"marker", "files", "user"} {
		t.Run(alias, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			expected := filepath.Join(root, "user", "workspaces", "workspace", "files")
			if err := os.MkdirAll(expected, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			aliasPath, destination := filepath.Join(expected, ".agent-remote-workspace.json"), target
			if alias == "files" {
				aliasPath, destination = expected, outside
			}
			if alias == "user" {
				aliasPath, destination = filepath.Join(root, "user"), outside
			}
			if err := os.RemoveAll(aliasPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, aliasPath); err != nil {
				t.Fatal(err)
			}
			_, err := Prepare(root, PreparePayload{UserID: "user", WorkspaceID: "workspace", SyncSessionID: "sync", RemotePath: expected})
			if alias != "marker" && err == nil {
				t.Fatal("directory symlink accepted")
			}
			data, readErr := os.ReadFile(target)
			if readErr != nil || string(data) != "untouched" {
				t.Fatalf("external sentinel changed: %q %v", data, readErr)
			}
			if alias != "marker" {
				if _, err := os.Stat(filepath.Join(outside, ".agent-remote-workspace.json")); !os.IsNotExist(err) {
					t.Fatal("external marker created")
				}
			}
		})
	}
}

func TestPrepareRejectsDifferentUserWorkspaceAndInvalidIDs(t *testing.T) {
	root := t.TempDir()
	for _, payload := range []PreparePayload{
		{UserID: "user", WorkspaceID: "workspace", SyncSessionID: "sync", RemotePath: filepath.Join(root, "other", "workspaces", "workspace", "files")},
		{UserID: "../outside", WorkspaceID: "workspace", SyncSessionID: "sync"},
		{UserID: "user", WorkspaceID: "../outside", SyncSessionID: "sync"},
		{UserID: "user", WorkspaceID: "workspace", SyncSessionID: "../outside"},
	} {
		if _, err := Prepare(root, payload); err == nil {
			t.Fatalf("unsafe payload accepted: %#v", payload)
		}
	}
}

func TestPinnedWorkspaceMarkerSurvivesAncestorReplacement(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	payload := PreparePayload{UserID: "user", WorkspaceID: "workspace", SyncSessionID: "sync"}
	directory, err := OpenDirectory(root, payload)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	retained := filepath.Join(root, "retained")
	if err := os.Rename(filepath.Join(root, "user"), retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "user")); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.WriteMarker(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(retained, "workspaces", "workspace", "files", ".agent-remote-workspace.json")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replacement path was touched: %v %v", entries, err)
	}
}
