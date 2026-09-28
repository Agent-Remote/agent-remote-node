package skillmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestFinalizationInventoryPagesWithoutFollowingOrCreatingEntries(t *testing.T) {
	store, _ := privateStore(t)
	want := make([]string, FinalizationPageLimit*2+3)
	for i := len(want) - 1; i >= 0; i-- {
		id := fmt.Sprintf("%08x-1111-4111-8111-111111111111", i+1)
		want[i] = id
		// Directory names only: linked and incomplete candidates must still be inspected by callers.
		if i%2 == 0 {
			if err := store.Symlink("/missing", sessionBundleName(id)); err != nil {
				t.Fatal(err)
			}
		} else if err := store.Mkdir(sessionBundleName(id), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Mkdir("session-bad", 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Mkdir(".finalize-unrelated", 0700); err != nil {
		t.Fatal(err)
	}
	var got []string
	cursor := ""
	for {
		ids, more, invalid, err := ListFinalizationSessions(context.Background(), store, cursor)
		if err != nil || !invalid || len(ids) > FinalizationPageLimit {
			t.Fatal(ids, more, invalid, err)
		}
		got = append(got, ids...)
		if !more {
			break
		}
		cursor = ids[len(ids)-1]
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("inventory lost or repeated canonical names", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := ListFinalizationSessions(ctx, store, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled scan continued", err)
	}
	if _, _, _, err := ListFinalizationSessions(context.Background(), store, "../other"); err == nil {
		t.Fatal("path cursor accepted")
	}
	if _, err := store.Lstat(".finalize-unrelated"); err != nil {
		t.Fatal("scan mutated unrelated state", err)
	}
}

func TestExistingStateInventoryDistinguishesAbsentFromUnsafe(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root Linux store")
	}
	root := t.TempDir()
	if store, err := OpenExistingStateStore(root + "/missing/child"); !errors.Is(err, ErrStateStoreAbsent) || store != nil {
		t.Fatal("absent store was not classified", err)
	}
	if _, err := os.Stat(root + "/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only opener created state", err)
	}
	if err := os.Symlink(root+"/missing", root+"/linked"); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenExistingStateStore(root + "/linked"); err == nil || errors.Is(err, ErrStateStoreAbsent) || store != nil {
		t.Fatal("link masqueraded as absent store", err)
	}
}

func TestFinalizationInventoryRecognizesLaunchJournals(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, kind := range []string{"regular", "symlink", "directory", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			store, _ := privateStore(t)
			if err := store.Mkdir(sessionBundleName(id), 0700); err != nil {
				t.Fatal(err)
			}
			name := "session-launch-" + id + ".json"
			var err error
			switch kind {
			case "regular", "malformed":
				if kind == "malformed" {
					name = "session-launch-invalid.json"
				}
				err = store.WriteFile(name, []byte("{}"), 0600)
			case "symlink":
				err = store.Symlink("/missing", name)
			case "directory":
				err = store.Mkdir(name, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			ids, more, invalid, err := ListFinalizationSessions(context.Background(), store, "")
			if err != nil || more || !reflect.DeepEqual(ids, []string{id}) || invalid != (kind != "regular") {
				t.Fatalf("launch journal classification: ids=%v more=%v invalid=%v err=%v", ids, more, invalid, err)
			}
		})
	}
}
