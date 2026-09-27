package ledger

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLedgerCompareAndSwapDoesNotOverwriteConcurrentConfirmation(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := Entry{TaskID: "original", Status: "pending", Result: map[string]any{"attempt": 3}}
	if err := store.CompareAndSwap(nil, original); err != nil {
		t.Fatal(err)
	}
	if err := store.CompareAndSwap(nil, original); !errors.Is(err, ErrConflict) {
		t.Fatal("existing intent replaced", err)
	}
	expected, ok, err := store.Get(original.TaskID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var workers sync.WaitGroup
	for _, status := range []string{"accepted", "superseded"} {
		workers.Go(func() {
			changed := original
			changed.Status = status
			if err := store.CompareAndSwap(&expected, changed); err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if winners.Load() != 1 {
		t.Fatal("stale confirmation overwrote a new record")
	}
	saved, _, err := store.Get(original.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(saved.Status)
	if err != nil || len(listed) != 1 {
		t.Fatal("pending recovery could not enumerate exact records", err)
	}
	listed[0].Result["attempt"] = 9
	if err := store.CompareAndSwap(&saved, original); err != nil {
		t.Fatal("list leaked mutable record state", err)
	}
}
