package skillmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sessionReceipt(t *testing.T, source Manifest, options MaterializeOptions) SessionSnapshot {
	t.Helper()
	digest, err := Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	return SessionSnapshot{
		Snapshot: PreparedSnapshot{Version: 1, Binding: SnapshotBinding{
			UserID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222",
			NodeID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444",
			SnapshotID: "55555555-5555-4555-8555-555555555555", DirectoryEpoch: 1, LibraryGeneration: 2, InitialTreeDigest: digest,
		}, Capture: CaptureOptions{DirectoryBytes: options.Policy.DirectoryBytes, Entries: options.Policy.Entries}},
		Runtime: RuntimeBinding{Backend: "native", ResourceID: "session.service", BootID: "66666666-6666-4666-8666-666666666666", UID: options.UID, GID: options.GID},
	}
}

func TestSessionPreparationSealsBindingBeforePublicationAndNeverOverwritesEdits(t *testing.T) {
	store, path := privateStore(t)
	content := []byte("original")
	entry := objectEntry("auxiliary", content, 0o444)
	source := Manifest{Version: 1, Entries: []Entry{entry}}
	options := runtimeOptions()
	receipt := sessionReceipt(t, source, options)
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(map[string][]byte{entry.SHA256: content}), receipt, options); err != nil {
		t.Fatal(err)
	}
	sessionID := receipt.Snapshot.Binding.SessionID
	bundle, actual, err := OpenSessionSnapshot(store, sessionID)
	if err != nil || !reflect.DeepEqual(receipt, actual) {
		t.Fatalf("missing complete preparation receipt: %#v, %v", actual, err)
	}
	defer bundle.Close()
	work, err := OpenSessionWork(bundle, receipt.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	_ = work.Close()
	dataPath := filepath.Join(path, sessionBundleName(sessionID), "work", "auxiliary")
	if err := os.WriteFile(dataPath, []byte("learned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), receipt, options); err != nil {
		t.Fatal("identical retry should not read objects", err)
	}
	if data, err := os.ReadFile(dataPath); err != nil || string(data) != "learned" {
		t.Fatal("retry replaced runtime changes", err)
	}
	changed := receipt
	changed.Runtime.BootID = changed.Snapshot.Binding.NodeID
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), changed, options); err == nil {
		t.Fatal("reboot repurposed old snapshot")
	}
	changed = receipt
	changed.Snapshot.Binding.AccountID = changed.Snapshot.Binding.UserID
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), changed, options); err == nil {
		t.Fatal("different account reused retained state")
	}
}

func TestSessionPreparationFailureNeverPublishesAnUnsealedBundle(t *testing.T) {
	store, _ := privateStore(t)
	entry := objectEntry("auxiliary", []byte("required"), 0o600)
	source := Manifest{Version: 1, Entries: []Entry{entry}}
	options := runtimeOptions()
	receipt := sessionReceipt(t, source, options)
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), receipt, options); err == nil {
		t.Fatal("missing object accepted")
	}
	if _, _, err := OpenSessionSnapshot(store, receipt.Snapshot.Binding.SessionID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preparation published a bundle: %v", err)
	}
	directory, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	names, err := directory.Readdirnames(-1)
	if err != nil || len(names) != 0 {
		t.Fatalf("failed preparation retained incomplete staging: %v, %v", names, err)
	}
}

func TestRetainedSessionCorruptionCannotMasqueradeAsMissingSession(t *testing.T) {
	for _, name := range []string{"runtime.json", "snapshot.json", "baseline.json"} {
		t.Run(name, func(t *testing.T) {
			store, _ := privateStore(t)
			source := Manifest{Version: 1}
			options := runtimeOptions()
			receipt := sessionReceipt(t, source, options)
			if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), receipt, options); err != nil {
				t.Fatal(err)
			}
			if err := store.Remove(filepath.Join(sessionBundleName(receipt.Snapshot.Binding.SessionID), name)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := OpenSessionSnapshot(store, receipt.Snapshot.Binding.SessionID); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete binding looks recoverably absent: %v", err)
			}
			if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), receipt, options); err == nil {
				t.Fatal("retry silently resealed corrupted state")
			}
		})
	}
}
