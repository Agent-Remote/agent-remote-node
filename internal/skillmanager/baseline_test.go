package skillmanager

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestPermissionBaselineDoesNotInventSessionEdits(t *testing.T) {
	source := Manifest{Version: 1, Entries: []Entry{
		{Path: "scripts", Kind: "directory", Mode: 0o500},
		{Path: "scripts/run", Kind: "file", Mode: 0o555, SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", ContentKind: "text"},
	}}
	baseline, err := WritableBaseline(source)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Materialized.Entries[0].Mode != 0o700 || baseline.Materialized.Entries[1].Mode != 0o755 {
		t.Fatal("copy modes do not permit owner writes")
	}
	final, err := RestoreSourceModes(baseline.Materialized, baseline)
	if err != nil || !equalManifests(final, source) {
		t.Fatalf("copy preparation became a runtime edit: %v", err)
	}
	changed := cloneManifest(baseline.Materialized)
	changed.Entries[1].Mode = 0o700
	final, err = RestoreSourceModes(changed, baseline)
	if err != nil || final.Entries[1].Mode != 0o700 {
		t.Fatalf("real chmod was discarded: %v", err)
	}
	if source.Entries[1].Mode != 0o555 {
		t.Fatal("baseline mutated the immutable source")
	}
}

func TestPermissionBaselineRejectsForgedActualModes(t *testing.T) {
	source := Manifest{Version: 1, Entries: []Entry{{Path: "directory", Kind: "directory", Mode: 0o555}}}
	baseline, err := WritableBaseline(source)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Materialized.Entries[0].Mode = 0o777
	if _, err := RestoreSourceModes(source, baseline); err == nil {
		t.Fatal("unverified actual-mode baseline accepted")
	}
}

type shortBaselineWriter struct{ bytes.Buffer }

func (w *shortBaselineWriter) Write(data []byte) (int, error) {
	return w.Buffer.Write(data[:min(3, len(data))])
}

type stalledBaselineWriter struct{}

func (stalledBaselineWriter) Write([]byte) (int, error) { return 0, nil }

func TestStreamingBaselinePreservesCompleteJSONAcrossShortWrites(t *testing.T) {
	source := Manifest{Version: 1, Entries: []Entry{{Path: "quoted\"directory", Kind: "directory", Mode: 0o555}}}
	baseline, err := WritableBaseline(source)
	if err != nil {
		t.Fatal(err)
	}
	writer := &shortBaselineWriter{}
	if err := writePermissionBaseline(writer, baseline); err != nil {
		t.Fatal(err)
	}
	var decoded PermissionBaseline
	if err := json.Unmarshal(writer.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != baseline.Version || !equalManifests(decoded.Source, baseline.Source) || !equalManifests(decoded.Materialized, baseline.Materialized) {
		t.Fatal("streamed baseline changed the permission contract")
	}
	if err := writePermissionBaseline(stalledBaselineWriter{}, baseline); err != io.ErrShortWrite {
		t.Fatalf("stalled writer was not rejected: %v", err)
	}
}
