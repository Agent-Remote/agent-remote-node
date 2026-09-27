package skillmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// These opt-in cases exercise real published limits rather than scaled-down policy values.
func TestSkillDefaultCapacity(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST") != "1" {
		t.Skip("requires isolated Linux storage for the published runtime limits")
	}
	if os.Geteuid() != 0 {
		t.Fatal("capacity acceptance requires root to materialize the non-root runtime identity")
	}
	mode := os.Getenv("AGENT_REMOTE_SKILL_CAPACITY_MODE")
	if mode != "bytes" && mode != "entries" {
		t.Fatal("select explicit bytes or entries capacity mode")
	}
	if mode == "entries" {
		t.Run("100000_unique_files", func(t *testing.T) {
			source, objects := capacityEntries()
			verifyCapacityLifecycle(t, source, objects)
		})
		return
	}
	for _, count := range []int{1, 10} {
		t.Run(fmt.Sprintf("%d_GiB", count), func(t *testing.T) {
			source, objects := capacityBytes(t, count)
			verifyCapacityLifecycle(t, source, objects)
		})
	}
}

type capacityObject struct {
	data []byte
	size int64
	fill byte
}

func (o capacityObject) open() io.ReadCloser {
	if o.data != nil {
		return io.NopCloser(bytes.NewReader(o.data))
	}
	return io.NopCloser(io.LimitReader(capacityFill(o.fill), o.size))
}

type capacityFill byte

func (value capacityFill) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(value)
	}
	return len(buffer), nil
}

func capacityBytes(t *testing.T, count int) (Manifest, map[string]capacityObject) {
	t.Helper()
	source := Manifest{Version: 1}
	objects := make(map[string]capacityObject)
	for index := range count {
		name := fmt.Sprintf("skill-%02d", index)
		instructions := []byte(fmt.Sprintf("---\nname: %s\ndescription: capacity acceptance\n---\n", name))
		metadata := objectEntry(name+"/SKILL.md", instructions, 0o444)
		object := capacityObject{size: DefaultStatePolicy().CheckpointBytes - metadata.Size, fill: byte(128 + index)}
		hash := sha256.New()
		reader := object.open()
		_, err := io.Copy(hash, reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		entry := Entry{Path: name + "/state.db", Kind: "file", Mode: 0o444, Size: object.size, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentKind: "binary"}
		source.Entries = append(source.Entries, Entry{Path: name, Kind: "directory", Mode: 0o555}, metadata, entry)
		objects[metadata.SHA256] = capacityObject{data: instructions}
		objects[entry.SHA256] = object
	}
	return source, objects
}

func capacityEntries() (Manifest, map[string]capacityObject) {
	source := Manifest{Version: 1}
	objects := make(map[string]capacityObject)
	for index := range DefaultStatePolicy().Entries {
		data := []byte(fmt.Sprintf("unique capacity object %06d\n", index))
		entry := objectEntry(fmt.Sprintf("state-%06d", index), data, 0o444)
		source.Entries = append(source.Entries, entry)
		objects[entry.SHA256] = capacityObject{data: data}
	}
	return source, objects
}

func verifyCapacityLifecycle(t *testing.T, source Manifest, objects map[string]capacityObject) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	store, path := privateStore(t)
	options := runtimeOptions()
	receipt := sessionReceipt(t, source, options)
	opener := func(ctx context.Context, digest string) (io.ReadCloser, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		object, ok := objects[digest]
		if !ok {
			return nil, errors.New("capacity fixture requested unknown content")
		}
		return object.open(), nil
	}
	started := time.Now()
	if err := PrepareSessionSnapshot(ctx, store, source, opener, receipt, options); err != nil {
		t.Fatal(err)
	}
	capacityPhase(t, "materialize", started)
	bundle, _, err := OpenSessionSnapshot(store, receipt.Snapshot.Binding.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	started = time.Now()
	record, err := FinalizeWorkTreeWithPolicy(ctx, bundle, receipt.Snapshot.Binding, false, options.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if record.TreeDigest != receipt.Snapshot.Binding.InitialTreeDigest || record.Unclean || record.State != "local_durable" {
		t.Fatal("capacity capture changed source permissions/content or claimed remote persistence")
	}
	capacityPhase(t, "freeze", started)
	started = time.Now()
	verifyCapacityObjects(t, bundle, source, record)
	capacityPhase(t, "verify_all_objects", started)
	// Reopen the actual store to verify durable receipts independently of preparation handles.
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(filepath.Join(path, sessionBundleName(receipt.Snapshot.Binding.SessionID)))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := FinalizeWorkTreeWithPolicy(ctx, reopened, receipt.Snapshot.Binding, true, options.Policy)
	if err != nil || actual != record {
		t.Fatal("reopened retry changed the original capacity capture", err)
	}
}

func verifyCapacityObjects(t *testing.T, bundle *os.Root, source Manifest, record FinalizationRecord) {
	t.Helper()
	file, actual, err := OpenFinalizationManifest(bundle, record.Binding)
	if err != nil || actual != record {
		t.Fatal("capacity manifest cannot be read through the transfer boundary", err)
	}
	defer file.Close()
	var frozen Manifest
	if err := json.NewDecoder(file).Decode(&frozen); err != nil || !equalManifests(frozen, source) {
		t.Fatal("frozen capacity manifest differs from original complete input", err)
	}
	objects, err := bundle.OpenRoot("finalization/objects")
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	var total int64
	buffer := make([]byte, 64<<10)
	for _, entry := range source.Entries {
		if entry.Kind != "file" {
			continue
		}
		object, err := objects.Open(entry.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		size, err := io.CopyBuffer(hash, struct{ io.Reader }{object}, buffer)
		_ = object.Close()
		if err != nil || size != entry.Size || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			t.Fatal("retained capacity object was truncated or changed", err)
		}
		total += size
	}
	t.Logf("verified entries=%d expanded_bytes=%d", len(source.Entries), total)
}

func capacityPhase(t *testing.T, phase string, started time.Time) {
	t.Helper()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	t.Logf("phase=%s elapsed=%s heap_inuse_bytes=%d total_alloc_bytes=%d peak_rss_kib=%d", phase, time.Since(started).Round(time.Millisecond), stats.HeapInuse, stats.TotalAlloc, usage.Maxrss)
}
