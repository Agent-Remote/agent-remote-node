package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLedgerFailedPublicationPreservesOriginal(t *testing.T) {
	for _, stage := range []string{"encoding", "write", "rename"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			original := Entry{TaskID: "original", Status: "succeeded", Result: map[string]any{"ok": true}}
			if err := store.Save(original); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			next := Entry{TaskID: "original", Status: "failed"}
			switch stage {
			case "encoding":
				next.Error = map[string]any{"unsupported": make(chan int)}
			case "write":
				store.publish = func(string, []byte) (bool, error) {
					return false, errors.New("injected write failure")
				}
			case "rename":
				if err := os.Mkdir(path+".directory", 0o700); err != nil {
					t.Fatal(err)
				}
				store.path = path + ".directory"
			}
			if err := store.Save(next); err == nil {
				t.Fatal("failed publication was accepted")
			}
			saved, ok, err := store.Get("original")
			if err != nil || !ok || saved.Status != "succeeded" {
				t.Fatal("failed write changed memory", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed write changed disk", err)
			}
		})
	}
}

func TestLedgerUncertainDirectorySyncRequiresReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store.publish = func(path string, data []byte) (bool, error) {
		renamed, err := publish(path, data)
		if err != nil {
			return renamed, err
		}
		return true, errors.New("injected directory sync uncertainty")
	}
	entry := Entry{TaskID: "original", Status: "succeeded"}
	if err := store.Save(entry); err == nil {
		t.Fatal("uncertain durability was accepted")
	}
	if _, _, err := store.Get(entry.TaskID); err == nil {
		t.Fatal("uncertain ledger authorized task replay")
	}
	if err := store.Save(Entry{TaskID: "replacement", Status: "failed"}); err == nil {
		t.Fatal("uncertain ledger allowed another write")
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok, err := reopened.Get(entry.TaskID)
	if err != nil || !ok || saved.Status != entry.Status {
		t.Fatal("reopen lost original publication", err)
	}
}

func TestLedgerOwnsDetachedJSONValues(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	nested := map[string]any{"value": "original"}
	entry := Entry{TaskID: "original", Result: map[string]any{"items": []any{nested}}}
	if err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	nested["value"] = "changed input"
	for range 2 {
		saved, ok, err := store.Get(entry.TaskID)
		if err != nil || !ok {
			t.Fatal("missing entry", err)
		}
		value := saved.Result["items"].([]any)[0].(map[string]any)
		if value["value"] != "original" {
			t.Fatal("mutable caller value changed ledger")
		}
		value["value"] = "changed output"
	}
}

func TestLedgerAtomicReadersAndPrivatePublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Entry{TaskID: "original", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	done := make(chan struct{})
	readers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			reopened, err := Open(path)
			if err != nil {
				t.Error("reader observed partial ledger", err)
				return
			}
			if _, ok, err := reopened.Get("original"); err != nil || !ok {
				t.Error("reader lost existing result", err)
				return
			}
		}
	})
	for range 25 {
		if err := store.Save(Entry{TaskID: "next", Status: "succeeded"}); err != nil {
			t.Error(err)
			break
		}
	}
	close(done)
	readers.Wait()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("ledger is not private", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("publication left temporary files", err)
	}
}

func TestLedgerCorruptionIsNotAnEmptyHistory(t *testing.T) {
	for _, content := range []string{"", "null", "{", "[]"} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("corrupt history permits task execution")
			}
		})
	}
}

func TestLedgerPreservesLargeIntegerThroughReopenAndRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(Entry{TaskID: "original", Result: map[string]any{"generation": int64(9223372036854775806)}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(Entry{TaskID: "another"}); err != nil {
			t.Fatal(err)
		}
		entry, ok, err := store.Get("original")
		if err != nil || !ok {
			t.Fatal("original receipt missing", err)
		}
		data, err := json.Marshal(entry.Result)
		if err != nil || string(data) != `{"generation":9223372036854775806}` {
			t.Fatal("ledger changed original integer", err)
		}
	}
}
