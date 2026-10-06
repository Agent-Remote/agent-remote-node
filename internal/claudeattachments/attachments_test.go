package claudeattachments

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestArgumentsPreservePromptAndUserDirectories(t *testing.T) {
	for _, args := range [][]string{nil, {"review this"}, {"--add-dir", "/user-dir", "--model", "opus", "review this"}, {"--", "--literal-prompt"}} {
		original := append([]string(nil), args...)
		got := Arguments(args, "/account/.agent-remote-attachments/session")
		index := len(args)
		for i, arg := range args {
			if arg == "--" {
				index = i
				break
			}
		}
		if got[index] != "--add-dir=/account/.agent-remote-attachments/session" {
			t.Fatal(got)
		}
		restored := append(append([]string(nil), got[:index]...), got[index+1:]...)
		if !reflect.DeepEqual(restored, original) || !reflect.DeepEqual(args, original) {
			t.Fatalf("arguments changed: %q", got)
		}
	}
}

func TestPrepareCreatesIsolatedDirectoryAndPreservesFiles(t *testing.T) {
	account := t.TempDir()
	dir := Directory(account, "session")
	if err := Prepare(account, "session", os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "existing")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(account, "session", os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatal("existing attachment changed", err)
	}
	if _, err := os.Stat(Directory(account, "other")); !os.IsNotExist(err) {
		t.Fatal("unrelated session created")
	}
}

func TestPrepareRejectsDirectoryAliasesAndInvalidIdentity(t *testing.T) {
	for _, component := range []string{"account", "namespace", "session", "file"} {
		t.Run(component, func(t *testing.T) {
			root := t.TempDir()
			account := filepath.Join(root, "account")
			outside := filepath.Join(root, "outside")
			for _, p := range []string{account, outside} {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(account, ".agent-remote-attachments")
			switch component {
			case "account":
				if err := os.Remove(account); err != nil {
					t.Fatal(err)
				}
				link = account
			case "session":
				if err := os.Mkdir(link, 0700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, "session")
			case "file":
				if err := os.WriteFile(link, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if component != "file" {
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
			}
			if err := Prepare(account, "session", os.Getuid(), os.Getgid()); err == nil {
				t.Fatal("accepted unsafe directory")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("modified link target", err)
			}
		})
	}
	for _, id := range []string{"", ".", "..", "../other", "a/b", "a\\b"} {
		account := t.TempDir()
		if err := Prepare(account, id, 0, 0); err == nil {
			t.Fatalf("accepted %q", id)
		}
		entries, _ := os.ReadDir(account)
		if len(entries) != 0 {
			t.Fatal("invalid identity created state")
		}
	}
}
