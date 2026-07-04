package ledger

import "testing"

func TestLedgerPersistsEntries(t *testing.T) {
	path := t.TempDir() + "/ledger.json"
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(Entry{TaskID: "task_1", Status: "succeeded", Result: map[string]any{"ok": true}}); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := second.Get("task_1")
	if !ok {
		t.Fatal("expected entry")
	}
	if entry.Status != "succeeded" {
		t.Fatalf("unexpected status: %s", entry.Status)
	}
}
