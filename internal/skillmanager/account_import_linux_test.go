package skillmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func accountFenceFixture() AccountFence {
	return AccountFence{Version: 1, NodeID: "33333333-3333-4333-8333-333333333333", UserID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222", DirectoryEpoch: 1}
}

func accountImportFixture() AccountImportReceipt {
	fence := accountFenceFixture()
	return AccountImportReceipt{Version: 1, NodeID: fence.NodeID, UserID: fence.UserID, AccountID: fence.AccountID,
		TaskID: "import_tool_account_config:" + fence.AccountID + ":44444444-4444-4444-8444-444444444444", InputDigest: strings.Repeat("a", 64), State: "started"}
}

func TestAccountFenceSurvivesReopenAndCannotChangeOwner(t *testing.T) {
	store, path := privateStore(t)
	binding := accountFenceFixture()
	if _, err := ReadAccountFence(store, binding.NodeID, binding.UserID, binding.AccountID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected fresh fence: %v", err)
	}
	if _, err := CloseAccountImports(store, binding); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	newer := binding
	newer.DirectoryEpoch = 8
	actual, err := CloseAccountImports(reopened, newer)
	if err != nil || actual != binding {
		t.Fatalf("fence was replaced: %#v %v", actual, err)
	}
	newer.UserID = binding.NodeID
	if _, err := CloseAccountImports(reopened, newer); err == nil {
		t.Fatal("fence was transferred to another owner")
	}
}

func TestAccountFenceCorruptionCannotReopenImports(t *testing.T) {
	for _, corrupt := range []string{"json", "mode", "link", "valid_link", "identity"} {
		t.Run(corrupt, func(t *testing.T) {
			store, path := privateStore(t)
			binding := accountFenceFixture()
			if _, err := CloseAccountImports(store, binding); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(path, "account-"+binding.AccountID+".json")
			switch corrupt {
			case "json":
				if err := os.WriteFile(name, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(name, 0o644); err != nil {
					t.Fatal(err)
				}
			case "link", "valid_link":
				target := "missing"
				if err := os.Rename(name, filepath.Join(path, "original.json")); err != nil {
					t.Fatal(err)
				}
				if corrupt == "valid_link" {
					target = "original.json"
				}
				if err := os.Symlink(target, name); err != nil {
					t.Fatal(err)
				}
			case "identity":
				binding.NodeID = binding.UserID
			}
			if _, err := ReadAccountFence(store, binding.NodeID, binding.UserID, binding.AccountID); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsafe fence was accepted or treated as absent: %v", err)
			}
			if _, err := CloseAccountImports(store, binding); err == nil {
				t.Fatal("invalid fence was silently recreated")
			}
		})
	}
}

func TestImportReceiptKeepsStartedAndRejectsChangedInput(t *testing.T) {
	store, path := privateStore(t)
	input := accountImportFixture()
	if err := BeginAccountImport(store, input); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read, err := ReadAccountImport(reopened, input)
	if err != nil || read.State != "started" {
		t.Fatalf("lost in-flight intent: %#v %v", read, err)
	}
	changed := input
	changed.InputDigest = strings.Repeat("b", 64)
	if _, err := ReadAccountImport(reopened, changed); err == nil {
		t.Fatal("changed content reused an import task")
	}
	if err := BeginAccountImport(reopened, input); err == nil {
		t.Fatal("started receipt was overwritten")
	}
	if err := FinishAccountImport(reopened, input, "succeeded"); err != nil {
		t.Fatal(err)
	}
	if err := FinishAccountImport(reopened, input, "succeeded"); err != nil {
		t.Fatal(err)
	}
	if err := FinishAccountImport(reopened, input, "failed"); err == nil {
		t.Fatal("terminal classification was rewritten")
	}
	read, err = ReadAccountImport(reopened, input)
	if err != nil || read.State != "succeeded" {
		t.Fatalf("lost terminal receipt: %#v %v", read, err)
	}
}

func TestAccountImportDrainRequiresTerminalLocalReceipt(t *testing.T) {
	for _, state := range []string{"started", "succeeded", "failed"} {
		t.Run(state, func(t *testing.T) {
			store, _ := privateStore(t)
			fence, receipt := accountFenceFixture(), accountImportFixture()
			if _, err := CloseAccountImports(store, fence); err != nil {
				t.Fatal(err)
			}
			if err := BeginAccountImport(store, receipt); err != nil {
				t.Fatal(err)
			}
			if state != "started" {
				if err := FinishAccountImport(store, receipt, state); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckAccountImportsDrained(store, fence.NodeID, fence.UserID, fence.AccountID)
			if (err != nil) != (state == "started") {
				t.Fatalf("wrong drain outcome: %s %v", state, err)
			}
		})
	}
}

func TestAccountImportDrainRejectsCorruptUnattributableRecord(t *testing.T) {
	store, path := privateStore(t)
	fence := accountFenceFixture()
	if _, err := CloseAccountImports(store, fence); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(path, "import-unknown.json")); err != nil {
		t.Fatal(err)
	}
	if err := CheckAccountImportsDrained(store, fence.NodeID, fence.UserID, fence.AccountID); err == nil {
		t.Fatal("unattributable corrupt receipt skipped")
	}
}
