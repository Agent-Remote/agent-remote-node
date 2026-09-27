package skillexport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type wireRecoverySource struct {
	entries  []skillmanager.Entry
	objects  map[string][]byte
	finalErr error
}

func (s *wireRecoverySource) Observation() skillmanager.RecoveryObservation {
	hasher := skillmanager.NewRecoveryHasher()
	for _, entry := range s.entries {
		if err := hasher.Add(entry); err != nil {
			panic(err)
		}
	}
	return hasher.Observation()
}
func (s *wireRecoverySource) Unclean() bool                { return true }
func (s *wireRecoverySource) Verify(context.Context) error { return s.finalErr }
func (s *wireRecoverySource) Stream(ctx context.Context, sink skillmanager.RecoverySink) error {
	for _, entry := range s.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := entry
		start.SHA256, start.ContentKind = "", ""
		writer, err := sink.Begin(start)
		if err != nil {
			return err
		}
		if entry.Kind == "file" {
			if _, err := writer.Write(s.objects[entry.SHA256]); err != nil {
				return err
			}
		}
		if err := sink.End(entry); err != nil {
			return err
		}
	}
	return nil
}

func recoveryWireFixture(t *testing.T) (*exportFixture, *wireRecoverySource, []byte) {
	t.Helper()
	fixture := frozenFixture(t)
	source := &wireRecoverySource{entries: fixture.manifest.Entries, objects: fixture.bytes}
	var output bytes.Buffer
	if err := WriteRecovery(context.Background(), &output, fixture.permission.Binding, source, source.Verify); err != nil {
		t.Fatal(err)
	}
	return fixture, source, output.Bytes()
}

func TestRecoveryWireVerifiesOrderedEntriesAndRepeatedFileContent(t *testing.T) {
	fixture, source, wire := recoveryWireFixture(t)
	golden, err := os.ReadFile("testdata/recovery-export-v1.bin")
	if err != nil || !bytes.Equal(wire, golden) {
		t.Fatal("recovery output differs from independent cross-language fixture", err)
	}
	var output bytes.Buffer
	checks, forced := 0, 0
	err = RelayRecovery(context.Background(), bytes.NewReader(wire), &output, func(header Header, force bool) error {
		checks++
		if force {
			forced++
		}
		if header.Binding != fixture.permission.Binding || header.TreeDigest != source.Observation().TreeDigest || !header.Unclean {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil || !bytes.Equal(output.Bytes(), wire) || forced != 2 || checks != 8 {
		t.Fatal("recovery wire lost full verification or authority boundaries", err, checks, forced)
	}
	if source.Observation().FileObjects != 3 || source.Observation().FileBytes != 17 {
		t.Fatal("recovery deduplicated path content")
	}
}

func TestRecoveryWireRejectsTruncationCorruptionAndUnverifiedCompletion(t *testing.T) {
	_, _, original := recoveryWireFixture(t)
	headerEnd := 8 + 4 + int(binary.BigEndian.Uint32(original[8:]))
	entryEnd := headerEnd + 4 + int(binary.BigEndian.Uint32(original[headerEnd:]))
	for _, kind := range []string{"magic", "header_size", "truncated_header", "truncated_entry", "truncated_content", "content", "hash", "classification", "footer", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			wire := append([]byte{}, original...)
			switch kind {
			case "magic":
				wire[0] ^= 1
			case "header_size":
				binary.BigEndian.PutUint32(wire[8:], 4097)
			case "truncated_header":
				wire = wire[:headerEnd-1]
			case "truncated_entry":
				wire = wire[:entryEnd-1]
			case "truncated_content":
				wire = wire[:entryEnd+1]
			case "content":
				wire[entryEnd] ^= 1
			case "hash":
				position := bytes.Index(wire[entryEnd:], []byte(`"sha256":"`)) + entryEnd + len(`"sha256":"`)
				wire[position] = 'g'
			case "classification":
				wire = bytes.Replace(wire, []byte(`"content_kind":"text"`), []byte(`"content_kind":"xxxx"`), 1)
			case "footer":
				wire = wire[:len(wire)-1]
			case "trailing":
				wire = append(wire, 1)
			}
			var output bytes.Buffer
			if err := RelayRecovery(context.Background(), bytes.NewReader(wire), &output, func(Header, bool) error { return nil }); err == nil || bytes.Contains(output.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("invalid recovery produced a successful footer")
			}
		})
	}
	fixture, source, _ := recoveryWireFixture(t)
	source.finalErr = errors.New("changed source")
	var output bytes.Buffer
	if err := WriteRecovery(context.Background(), &output, fixture.permission.Binding, source, source.Verify); err == nil || bytes.Contains(output.Bytes(), []byte(`"complete":true`)) {
		t.Fatal("unverified source completed")
	}
}

type negotiatedRecoveryFixture struct {
	*recoveryFixture
	source *wireRecoverySource
	called bool
}

func (f *negotiatedRecoveryFixture) StreamStoppedSkillRecovery(ctx context.Context, _ string, binding skillmanager.NodeExportBinding, output io.Writer, check func(Header, bool) error) error {
	f.called = true
	var wire bytes.Buffer
	if err := WriteRecovery(ctx, &wire, binding, f.source, f.source.Verify); err != nil {
		return err
	}
	return RelayRecovery(ctx, &wire, output, check)
}

func TestRecoveryFormatRequiresNegotiationAndLiveOriginalAuthority(t *testing.T) {
	for _, kind := range []string{"legacy", "recovery", "revoked", "known_capture", "changed_classification"} {
		t.Run(kind, func(t *testing.T) {
			base, source, _ := recoveryWireFixture(t)
			fixture := &negotiatedRecoveryFixture{recoveryFixture: &recoveryFixture{exportFixture: base}, source: source}
			request := map[string]any{"version": 1, "grant": exportGrant}
			if kind != "legacy" {
				request["recovery_version"] = 1
			}
			if kind == "revoked" {
				base.denyAt = 3
			}
			if kind == "known_capture" {
				digest := base.record.TreeDigest
				base.permission.IncomingDigest = &digest
				base.permission.Unclean = &base.record.Unclean
			}
			if kind == "changed_classification" {
				clean := false
				base.permission.Unclean = &clean
			}
			data, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			connection := &exportConnection{in: bytes.NewReader(data)}
			err = Serve(context.Background(), connection, base.identity(), base, fixture)
			if kind == "legacy" || kind == "recovery" {
				magic := Magic
				if kind == "recovery" {
					magic = RecoveryMagic
				}
				if err != nil || !bytes.HasPrefix(connection.out.Bytes(), []byte(magic)) || fixture.called != (kind == "recovery") {
					t.Fatal("negotiation changed the wrong stream", err)
				}
			} else if err == nil || bytes.Contains(connection.out.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("denied recovery completed")
			}
		})
	}
}

func TestRecoveryNegotiationRefusesUnknownOrAmbiguousVersion(t *testing.T) {
	for _, version := range []string{"null", "true", "0", "2", "1.0", `"1"`} {
		data := `{"version":1,"grant":"` + exportGrant + `","recovery_version":` + version + `}`
		if _, err := readGrant(context.Background(), &exportConnection{in: bytes.NewReader([]byte(data))}); err == nil {
			t.Fatal("invalid recovery negotiation accepted", version)
		}
	}
	data := `{"version":1,"grant":"` + exportGrant + `","recovery_version":1,"recovery_version":1}`
	if _, err := readGrant(context.Background(), &exportConnection{in: bytes.NewReader([]byte(data))}); err == nil {
		t.Fatal("duplicate recovery negotiation accepted")
	}
}
