package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func accountCopyFixture() AccountCopyReceipt {
	f := accountFenceFixture()
	task := "migrate_tool_account_runtime:" + f.AccountID + ":44444444-4444-4444-8444-444444444444"
	return AccountCopyReceipt{Version: 1, NodeID: f.NodeID, UserID: f.UserID, AccountID: f.AccountID, TaskID: task, InputDigest: strings.Repeat("b", 64), BootID: f.NodeID, Unit: AccountCopyUnit(task), State: "started"}
}
func TestAccountCopyReceiptDurabilityAndImmutableInput(t *testing.T) {
	root, path := privateStore(t)
	record := accountCopyFixture()
	if err := BeginAccountCopy(root, record); err != nil {
		t.Fatal(err)
	}
	again, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	saved, err := ReadAccountCopy(again, record)
	if err != nil || saved != record {
		t.Fatal(saved, err)
	}
	changed := record
	changed.InputDigest = strings.Repeat("c", 64)
	if _, err := ReadAccountCopy(again, changed); err == nil {
		t.Fatal("accepted different copy input")
	}
	if err := BeginAccountCopy(again, record); !errors.Is(err, os.ErrExist) {
		t.Fatal("overwrote retained intent", err)
	}
	if err := FinishAccountCopy(again, record, "copied"); err != nil {
		t.Fatal(err)
	}
	if err := FinishAccountCopy(again, record, "failed"); err == nil {
		t.Fatal("changed terminal outcome")
	}
	changed = record
	changed.BootID = record.UserID
	saved, err = ReadAccountCopy(again, changed)
	if err != nil || saved.BootID != record.BootID || saved.State != "copied" {
		t.Fatal(saved, err)
	}
	if err := FinishAccountCopy(again, changed, "copied"); err == nil {
		t.Fatal("new boot rewrote proof")
	}
}
func TestAccountCopyRejectsUnsafeRecordsAndBindingDrift(t *testing.T) {
	for _, kind := range []string{"json", "link", "mode", "identity", "unit", "boot", "task", "state"} {
		t.Run(kind, func(t *testing.T) {
			root, path := privateStore(t)
			record := accountCopyFixture()
			if err := BeginAccountCopy(root, record); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(path, accountCopyName(record.TaskID))
			switch kind {
			case "json":
				if err := os.WriteFile(filename, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "link":
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("absent", filename); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(filename, 0644); err != nil {
					t.Fatal(err)
				}
			case "identity":
				record.NodeID = record.UserID
			case "unit":
				record.Unit = "other.service"
			case "boot":
				record.BootID = "invalid"
			case "task":
				record.TaskID += "/escape"
			case "state":
				record.State = "ready"
			}
			if _, err := ReadAccountCopy(root, record); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsafe receipt became absent or valid", err)
			}
		})
	}
}

func TestAccountCopyHistoryRejectsLocalRecordsEvenAfterCopyExit(t *testing.T) {
	for _, state := range []string{"started", "copied", "failed"} {
		t.Run(state, func(t *testing.T) {
			root, _ := privateStore(t)
			fence := accountFenceFixture()
			record := accountCopyFixture()
			if _, err := CloseAccountImports(root, fence); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountCopyHistory(root, fence.NodeID, fence.UserID, fence.AccountID); err != nil {
				t.Fatal(err)
			}
			if err := BeginAccountCopy(root, record); err != nil {
				t.Fatal(err)
			}
			if state != "started" {
				if err := FinishAccountCopy(root, record, state); err != nil {
					t.Fatal(err)
				}
			}
			if err := CheckAccountCopyHistory(root, fence.NodeID, fence.UserID, fence.AccountID); err == nil {
				t.Fatal("copy exit became whole-migration proof")
			}
			other := fence
			other.AccountID = "55555555-5555-4555-8555-555555555555"
			if _, err := CloseAccountImports(root, other); err != nil {
				t.Fatal(err)
			}
			if err := CheckAccountCopyHistory(root, other.NodeID, other.UserID, other.AccountID); err != nil {
				t.Fatal("unrelated account blocked", err)
			}
		})
	}
}
