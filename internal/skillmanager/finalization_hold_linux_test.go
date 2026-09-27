package skillmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFinalizationReadHoldsExcludeMarkingWithoutAllocatingMetadata(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	before, err := os.ReadDir(filepath.Join(bundle.Name(), "finalization"))
	if err != nil {
		t.Fatal(err)
	}
	first, record, err := HoldFinalizationContent(bundle, capture.Binding)
	if err != nil || record != capture {
		t.Fatal("read hold changed capture", err)
	}
	defer first.Close()
	second, _, err := HoldFinalizationContent(bundle, capture.Binding)
	if err != nil {
		t.Fatal("parallel readers conflicted", err)
	}
	defer second.Close()
	if _, err := first.Write([]byte("overwrite")); err == nil {
		t.Fatal("read hold descriptor is writable")
	}
	flags, err := unix.FcntlInt(first.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("read hold can escape exec", err)
	}
	for _, closeOne := range []bool{false, true} {
		if closeOne {
			_ = first.Close()
		}
		if _, err := MarkFinalizationReclamation(context.Background(), bundle, capture, authority, time.Now().Add(time.Minute), "/runtime/original"); err == nil {
			t.Fatal("active reader permitted marking")
		}
		if _, err := bundle.Lstat("finalization/reclamation.json"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("reader conflict left deletion intent", err)
		}
	}
	after, err := os.ReadDir(filepath.Join(bundle.Name(), "finalization"))
	if err != nil {
		t.Fatal(err)
	}
	names := func(entries []os.DirEntry) []string {
		values := []string{}
		for _, entry := range entries {
			values = append(values, entry.Name())
		}
		return values
	}
	if !reflect.DeepEqual(names(before), names(after)) {
		t.Fatal("read-only hold allocated journal metadata")
	}
	_ = second.Close()
	intent := markReclamation(t, bundle, capture, authority)
	if hold, _, err := HoldFinalizationContent(bundle, capture.Binding); err == nil || hold != nil {
		t.Fatal("marked capture acquired a new reader")
	}
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizationReadHoldSurvivesSenderDescriptorAndBundleClosure(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	sender, _, err := HoldFinalizationContent(bundle, capture.Binding)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.FcntlInt(sender.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	receiver := os.NewFile(uintptr(fd), "surviving-reader")
	defer receiver.Close()
	_ = sender.Close()
	reopened, err := os.OpenRoot(bundle.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	_ = bundle.Close()
	if _, err := MarkFinalizationReclamation(context.Background(), reopened, capture, authority, time.Now().Add(time.Minute), "/runtime/original"); err == nil {
		t.Fatal("sender shutdown released receiver's kernel hold")
	}
	_ = receiver.Close()
	markReclamation(t, reopened, capture, authority)
}

func TestFinalizationObjectDescriptorBlocksDeletionUntilReaderCloses(t *testing.T) {
	bundle, capture, authority := reclamationBundle(t)
	var manifest Manifest
	journal, err := bundle.OpenRoot("finalization")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := readPrivateJSON(journal, "manifest.json", maxManifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	file, _, err := OpenFinalizationObject(bundle, capture, manifest.Entries[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	intent := markReclamation(t, bundle, capture, authority)
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err == nil {
		t.Fatal("outstanding object descriptor was reclaimed")
	}
	if _, err := bundle.Stat("work/learning"); err != nil {
		t.Fatal("object reader was checked after deleting work", err)
	}
	_ = file.Close()
	if err := ReclaimFinalizationContent(context.Background(), bundle, intent, reclamationQuiescent); err != nil {
		t.Fatal(err)
	}
}
