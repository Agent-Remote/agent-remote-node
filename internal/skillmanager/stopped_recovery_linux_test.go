package skillmanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func stoppedRecoveryFixture(t *testing.T) (*os.Root, SessionSnapshot, string) {
	t.Helper()
	store, path := privateStore(t)
	options := runtimeOptions()
	// This small metadata fixture can run on private tmpfs without testing disk admission.
	options.Policy.MinimumFreeBytes, options.Policy.ReservePercent = 0, 0
	source := Manifest{Version: 1}
	session := sessionReceipt(t, source, options)
	if err := PrepareSessionSnapshot(context.Background(), store, source, objectSource(nil), session, options); err != nil {
		t.Fatal(err)
	}
	bundle, _, err := OpenSessionSnapshot(store, session.Snapshot.Binding.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	work := filepath.Join(path, sessionBundleName(session.Snapshot.Binding.SessionID), "work")
	if err := os.WriteFile(filepath.Join(work, "file"), []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	return bundle, session, work
}

func consumeRecoveryEntry(entry Entry, input io.Reader) error {
	if entry.Kind != "file" {
		if input != nil {
			return errors.New("non-file exposed a reader")
		}
		return nil
	}
	_, err := io.Copy(io.Discard, input)
	return err
}

type recoveryTestSink struct {
	count   int64
	bytes   int64
	current *RecoveryFileVerifier
	onBegin func(Entry) error
}

func (s *recoveryTestSink) Begin(entry Entry) (io.Writer, error) {
	if err := ValidateRecoveryStart(entry); err != nil {
		return nil, err
	}
	if s.onBegin != nil {
		if err := s.onBegin(entry); err != nil {
			return nil, err
		}
	}
	if entry.Kind != "file" {
		return nil, nil
	}
	var err error
	s.current, err = NewRecoveryFileVerifier(entry)
	return s.current, err
}

func (s *recoveryTestSink) End(entry Entry) error {
	if entry.Kind == "file" {
		actual, err := s.current.Complete(entry.SHA256, entry.ContentKind)
		if err != nil || actual != entry {
			return errors.New("streamed content mismatch")
		}
		s.current = nil
	}
	s.count++
	s.bytes += entry.Size
	return nil
}

func TestStoppedRecoveryStreamValidatesProvisionalBytesAndMutation(t *testing.T) {
	bundle, session, work := stoppedRecoveryFixture(t)
	source, err := OpenStoppedWorkRecovery(context.Background(), bundle, session)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sink := &recoveryTestSink{}
	if err := source.Stream(context.Background(), sink); err != nil || sink.count != 1 || sink.bytes != 8 {
		t.Fatal("stream lost content", err)
	}
	if err := source.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink = &recoveryTestSink{onBegin: func(entry Entry) error {
		return os.WriteFile(filepath.Join(work, entry.Path), []byte("modified"), 0600)
	}}
	if err := source.Stream(context.Background(), sink); err == nil {
		t.Fatal("stream accepted callback mutation")
	}
}

func TestStoppedRecoveryRequiresCompleteConsumptionAndStableRetainedSource(t *testing.T) {
	bundle, session, work := stoppedRecoveryFixture(t)
	source, err := OpenStoppedWorkRecovery(context.Background(), bundle, session)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if !source.Unclean() || source.Observation().Entries != 1 || source.Observation().FileBytes != 8 {
		t.Fatal("missing termination invented clean evidence or lost source counts")
	}
	if err := source.Walk(context.Background(), func(Entry, io.Reader) error { return nil }); err == nil {
		t.Fatal("incomplete consumption was accepted")
	}
	if err := source.Walk(context.Background(), consumeRecoveryEntry); err != nil {
		t.Fatal(err)
	}
	if err := source.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink := &recoveryTestSink{}
	if err := source.Stream(context.Background(), sink); err != nil || sink.count != source.Observation().Entries || sink.bytes != source.Observation().FileBytes {
		t.Fatal("bounded streaming lost complete content", err)
	}
	if err := os.WriteFile(filepath.Join(work, "file"), []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := source.Verify(context.Background()); err == nil {
		t.Fatal("source mutation was accepted after streaming")
	}
	if _, err := bundle.Lstat("termination.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery created termination evidence", err)
	}
	if _, err := bundle.Lstat("finalization"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery created a finalization", err)
	}
}

func TestStoppedRecoveryRejectsChangedAuthorityAndExistingCapture(t *testing.T) {
	for _, change := range []string{"termination", "baseline", "snapshot", "finalization", "work"} {
		t.Run(change, func(t *testing.T) {
			bundle, session, _ := stoppedRecoveryFixture(t)
			if _, err := retainTermination(bundle, session.Snapshot.Binding, false); err != nil {
				t.Fatal(err)
			}
			source, err := OpenStoppedWorkRecovery(context.Background(), bundle, session)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			if source.Unclean() {
				t.Fatal("original clean termination lost")
			}
			switch change {
			case "termination", "baseline", "snapshot":
				if err := bundle.Remove(change + ".json"); err != nil {
					t.Fatal(err)
				}
			case "finalization":
				if err := bundle.Mkdir("finalization", 0o700); err != nil {
					t.Fatal(err)
				}
			case "work":
				if err := bundle.Rename("work", "retained-work"); err != nil {
					t.Fatal(err)
				}
				if err := bundle.Symlink("retained-work", "work"); err != nil {
					t.Fatal(err)
				}
			}
			if err := source.Walk(context.Background(), consumeRecoveryEntry); err == nil {
				t.Fatal("changed retained authority was accepted")
			}
			if err := source.Verify(context.Background()); err == nil {
				t.Fatal("changed retained authority verified")
			}
			if change != "termination" {
				if reopened, err := OpenStoppedWorkRecovery(context.Background(), bundle, session); err == nil {
					_ = reopened.Close()
					t.Fatal("corrupt original source was reopened")
				}
			}
		})
	}
}

func TestStoppedRecoveryRejectsCallbackMutationAndCancellation(t *testing.T) {
	bundle, session, work := stoppedRecoveryFixture(t)
	source, err := OpenStoppedWorkRecovery(context.Background(), bundle, session)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := source.Walk(ctx, consumeRecoveryEntry); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled recovery continued", err)
	}
	if err := source.Walk(context.Background(), func(entry Entry, input io.Reader) error {
		if err := os.WriteFile(filepath.Join(work, entry.Path), []byte("replaced"), 0o600); err != nil {
			return err
		}
		return consumeRecoveryEntry(entry, input)
	}); err == nil {
		t.Fatal("callback mutation produced a complete recovery")
	}
}

func TestStoppedRecoveryWalksOverEntrySourceWithoutManifestOrCopy(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_RECOVERY_CAPACITY") != "1" {
		t.Skip("set AGENT_REMOTE_RUN_SKILL_RECOVERY_CAPACITY=1 for actual over-entry source acceptance")
	}
	bundle, session, work := stoppedRecoveryFixture(t)
	const additional = 100_000
	for index := range additional {
		if err := os.WriteFile(filepath.Join(work, fmt.Sprintf("file-%06d", index)), []byte{byte(index)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, err := OpenStoppedWorkRecovery(context.Background(), bundle, session)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Observation().Entries != additional+1 || source.Observation().FileBytes != additional+8 {
		t.Fatal("over-entry source was truncated")
	}
	var count int64
	if err := source.Walk(context.Background(), func(entry Entry, input io.Reader) error {
		count++
		return consumeRecoveryEntry(entry, input)
	}); err != nil || count != additional+1 {
		t.Fatal("over-entry stream was incomplete", count, err)
	}
	if err := source.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Lstat("finalization"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery created a second capture", err)
	}
}
