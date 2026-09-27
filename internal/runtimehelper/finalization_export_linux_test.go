package runtimehelper

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// The isolated nonroot child composes the stream with the real credential-checked Helper socket.
// HTTP grant authentication is tested independently; this fixed authority has no external endpoint.
func verifyNonrootFrozenExport(t *testing.T, client Client, record skillmanager.FinalizationRecord) {
	t.Helper()
	b := record.Binding
	permission := skillmanager.NodeExportPermission{
		Binding: skillmanager.NodeExportBinding{SnapshotID: b.SnapshotID, SessionID: b.SessionID, UserID: b.UserID,
			AccountID: b.AccountID, NodeID: b.NodeID, TaskID: b.TaskID, LibraryGeneration: b.LibraryGeneration,
			DirectoryEpoch: b.DirectoryEpoch, InitialTreeDigest: b.InitialTreeDigest},
		DeviceID: "77777777-7777-4777-8777-777777777777", SSHKeyID: "88888888-8888-4888-8888-888888888888",
		ExpiresAt: time.Now().Add(time.Minute).UTC(), RecheckSeconds: 10, IncomingDigest: &record.TreeDigest, Unclean: &record.Unclean,
	}
	if err := permission.MatchCapture(record); err != nil {
		t.Fatal("fixture needs complete original preparation identity", err)
	}
	grant := "fixture=.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	handshake, err := json.Marshal(map[string]any{"version": 1, "grant": grant})
	if err != nil {
		t.Fatal(err)
	}
	connection := &frozenExportConnection{input: bytes.NewReader(handshake)}
	authority := &frozenExportAuthority{permission: permission, grant: grant}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	identity := skillexport.Identity{NodeID: b.NodeID, SnapshotID: b.SnapshotID, DeviceID: permission.DeviceID, SSHKeyID: permission.SSHKeyID}
	if err := skillexport.Serve(ctx, connection, identity, authority, client); err != nil {
		t.Fatal("unprivileged frozen export failed", err)
	}
	reader := bytes.NewReader(connection.output.Bytes())
	magic := make([]byte, len(skillexport.Magic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != skillexport.Magic {
		t.Fatal("missing wire identity", err)
	}
	var header skillexport.Header
	readFrozenExportFrame(t, reader, &header)
	if header.Binding != permission.Binding || header.TreeDigest != record.TreeDigest || header.Unclean != record.Unclean || header.FileObjects != 1 {
		t.Fatal("frozen identity changed")
	}
	if len(header.Manifest.Entries) != 1 {
		t.Fatal("unexpected retained fixture")
	}
	entry := header.Manifest.Entries[0]
	content := make([]byte, entry.Size)
	if _, err := io.ReadFull(reader, content); err != nil || skillmanager.VerifyContent(entry, content) != nil || string(content) != "original\x00\xff" {
		t.Fatal("retained object changed", err)
	}
	marker, err := reader.ReadByte()
	if err != nil || marker != 1 {
		t.Fatal("missing object verification", err)
	}
	var footer struct {
		Version     int    `json:"version"`
		TreeDigest  string `json:"tree_digest"`
		FileObjects int    `json:"file_objects"`
		Complete    bool   `json:"complete"`
	}
	readFrozenExportFrame(t, reader, &footer)
	if footer.Version != 1 || !footer.Complete || footer.TreeDigest != record.TreeDigest || footer.FileObjects != 1 || reader.Len() != 0 || authority.checks < 3 {
		t.Fatal("stream lost completion or reauthorization")
	}
}

type frozenExportAuthority struct {
	permission skillmanager.NodeExportPermission
	grant      string
	checks     int
}

func (a *frozenExportAuthority) VerifyNodeExport(ctx context.Context, node, snapshot, device, key, grant string) (skillmanager.NodeExportPermission, error) {
	a.checks++
	if ctx.Err() != nil || node != a.permission.Binding.NodeID || snapshot != a.permission.Binding.SnapshotID || device != a.permission.DeviceID || key != a.permission.SSHKeyID || grant != a.grant {
		return skillmanager.NodeExportPermission{}, skillexport.ErrUnavailable
	}
	return a.permission, nil
}

type frozenExportConnection struct {
	input  *bytes.Reader
	output bytes.Buffer
}

func (c *frozenExportConnection) Read(value []byte) (int, error)  { return c.input.Read(value) }
func (c *frozenExportConnection) Write(value []byte) (int, error) { return c.output.Write(value) }
func (c *frozenExportConnection) Close() error                    { return nil }

func readFrozenExportFrame(t *testing.T, reader *bytes.Reader, value any) {
	t.Helper()
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length > 65536 {
		t.Fatal("invalid fixture frame", err)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
