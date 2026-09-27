package skillexport

import (
	"math"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestExportHeaderAllowsBytesAboveRuntimeDefault(t *testing.T) {
	f := frozenFixture(t)
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{{
		Path: "large.db", Kind: "file", Mode: 0600, Size: (10 << 30) + 1,
		SHA256: strings.Repeat("a", 64), ContentKind: "binary",
	}}}
	header, err := SnapshotHeader(f.permission.Binding, manifest, false)
	if err != nil || header.FileObjects != 1 || header.Manifest.Entries[0].Size != (10<<30)+1 {
		t.Fatal("runtime byte admission prevented complete export metadata", err)
	}
}

func TestExportHeaderRejectsExpandedSizeOverflow(t *testing.T) {
	f := frozenFixture(t)
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{
		{Path: "a", Kind: "file", Mode: 0600, Size: math.MaxInt64, SHA256: strings.Repeat("a", 64), ContentKind: "binary"},
		{Path: "b", Kind: "file", Mode: 0600, Size: 1, SHA256: strings.Repeat("b", 64), ContentKind: "binary"},
	}}
	if _, err := SnapshotHeader(f.permission.Binding, manifest, false); err == nil {
		t.Fatal("overflowing export metadata was accepted")
	}
	if _, err := exportObjects(manifest); err == nil {
		t.Fatal("object enumeration wrapped the expanded byte count")
	}
}
