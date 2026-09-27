package skillexport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type recoveryFixture struct {
	*exportFixture
	blockInspection bool
	blockScan       bool
	finalErr        error
	permissionSeen  bool
}

func (f *recoveryFixture) InspectSkillFinalization(ctx context.Context, _, _, _ string) (skillmanager.FinalizationRecord, error) {
	if f.blockInspection {
		<-ctx.Done()
	}
	return skillmanager.FinalizationRecord{}, ErrUnavailable
}

func (f *recoveryFixture) StreamStoppedSkillExport(ctx context.Context, _ string, binding skillmanager.NodeExportBinding, output io.Writer, check func(Header, bool) error) error {
	if f.blockScan {
		<-ctx.Done()
		return ctx.Err()
	}
	if binding != f.permission.Binding {
		return ErrUnavailable
	}
	header, err := SnapshotHeader(binding, f.manifest, f.record.Unclean)
	if err != nil {
		return err
	}
	var source bytes.Buffer
	err = WriteSnapshot(ctx, &source, header, func(_ context.Context, entry skillmanager.Entry) (io.ReadCloser, error) {
		return os.Open(f.files[entry.SHA256])
	}, func(context.Context) error { return f.finalErr })
	if err != nil && f.finalErr == nil {
		return err
	}
	return RelaySnapshot(ctx, bytes.NewReader(source.Bytes()), output, func(h Header, force bool) error {
		f.permissionSeen = true
		return check(h, force)
	})
}

func TestExportStoppedRecoveryUsesSameVerifiedWire(t *testing.T) {
	f := &recoveryFixture{exportFixture: frozenFixture(t)}
	c := connection()
	if err := Serve(context.Background(), c, f.identity(), f, f); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/frozen-export-v1.bin")
	if err != nil || !bytes.Equal(c.out.Bytes(), golden) || !f.permissionSeen || f.checks.Load() < 3 {
		t.Fatal("recovery changed wire or lost live authorization", err)
	}
}

func TestExportStoppedRecoveryRefusesMissingFinalProofOrRevocation(t *testing.T) {
	for _, kind := range []string{"source_changed", "denied", "wrong_termination"} {
		t.Run(kind, func(t *testing.T) {
			f := &recoveryFixture{exportFixture: frozenFixture(t)}
			switch kind {
			case "source_changed":
				f.finalErr = errors.New("changed")
			case "denied":
				f.denyAt = 3
			case "wrong_termination":
				digest, unclean := f.record.TreeDigest, !f.record.Unclean
				f.permission.IncomingDigest, f.permission.Unclean = &digest, &unclean
			}
			c := connection()
			if err := Serve(context.Background(), c, f.identity(), f, f); err == nil || bytes.Contains(c.out.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("unverified recovery completed", err)
			}
		})
	}
}

func TestExportRevocationCoversInitialInspectionAndRecoveryScan(t *testing.T) {
	for _, inspect := range []bool{false, true} {
		f := &recoveryFixture{exportFixture: frozenFixture(t), blockInspection: inspect, blockScan: true}
		f.denyAt = 2
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		c := connection()
		err := Serve(ctx, c, f.identity(), f, f)
		if err == nil || ctx.Err() != nil || f.checks.Load() < 2 || c.out.Len() != 0 {
			t.Fatal("revocation did not cancel initial private read", inspect, err)
		}
		cancel()
	}
}

func TestRelayRejectsIncompleteOrCorruptPrivateStream(t *testing.T) {
	golden, err := os.ReadFile("testdata/frozen-export-v1.bin")
	if err != nil {
		t.Fatal(err)
	}
	headerEnd := len(Magic) + 4 + int(binary.BigEndian.Uint32(golden[len(Magic):]))
	for _, kind := range []string{"trailing", "truncated_footer", "truncated_object", "bad_content", "oversized_header", "aliased_header", "missing_header_field", "duplicate_header_field", "null_footer", "wrong_footer"} {
		t.Run(kind, func(t *testing.T) {
			data := append([]byte{}, golden...)
			switch kind {
			case "trailing":
				data = append(data, 1)
			case "truncated_footer":
				data = data[:len(data)-1]
			case "truncated_object":
				data = data[:headerEnd+1]
			case "bad_content":
				data[headerEnd] ^= 1
			case "oversized_header":
				binary.BigEndian.PutUint32(data[len(Magic):], maxManifestBytes+1)
			case "aliased_header":
				data = bytes.Replace(data, []byte(`"unclean"`), []byte(`"Unclean"`), 1)
			case "missing_header_field", "duplicate_header_field":
				header := data[len(Magic)+4 : headerEnd]
				if kind == "missing_header_field" {
					header = bytes.Replace(header, []byte(`"unclean":false,`), nil, 1)
				} else {
					header = bytes.Replace(header, []byte(`"unclean":false,`), []byte(`"unclean":false,"unclean":false,`), 1)
				}
				var stream bytes.Buffer
				stream.WriteString(Magic)
				_ = binary.Write(&stream, binary.BigEndian, uint32(len(header)))
				stream.Write(header)
				stream.Write(data[headerEnd:])
				data = stream.Bytes()
			case "null_footer":
				data = bytes.Replace(data, []byte(`"complete":true`), []byte(`"complete":null`), 1)
			case "wrong_footer":
				data = bytes.Replace(data, []byte(`"complete":true`), []byte(`"complete":false`), 1)
			}
			var output bytes.Buffer
			if err := RelaySnapshot(context.Background(), bytes.NewReader(data), &output, func(Header, bool) error { return nil }); err == nil || bytes.Contains(output.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("invalid private stream completed", err)
			}
		})
	}
}
