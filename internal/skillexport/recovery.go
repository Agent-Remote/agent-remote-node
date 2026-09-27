package skillexport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// StoppedStore serves only independently proven quiescent original work when no capture exists.
// check validates every object locally; true requires fresh remote proof before header/footer.
type StoppedStore interface {
	StreamStoppedSkillExport(ctx context.Context, requestID string, binding skillmanager.NodeExportBinding, output io.Writer, check func(Header, bool) error) error
}

// WriteSnapshot frames a complete read observation; verify must recheck its source before the footer.
// It does not persist content or represent the observation as a durable finalization.
func WriteSnapshot(ctx context.Context, output io.Writer, header Header, open func(context.Context, skillmanager.Entry) (io.ReadCloser, error), verify func(context.Context) error) error {
	objects, err := validateHeader(header)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, Magic); err != nil {
		return err
	}
	if err := writeFrame(output, header, maxManifestBytes); err != nil {
		return err
	}
	for _, entry := range objects {
		file, err := open(ctx, entry)
		if err != nil {
			return err
		}
		err = copyExportObject(ctx, output, file, entry)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if _, err := output.Write([]byte{1}); err != nil {
			return err
		}
	}
	if err := verify(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeFrame(output, completion{1, header.TreeDigest, len(objects), true}, 4096)
}

// SnapshotHeader validates and counts a complete observation without creating a durable record.
func SnapshotHeader(binding skillmanager.NodeExportBinding, manifest skillmanager.Manifest, unclean bool) (Header, error) {
	digest, err := skillmanager.Digest(manifest)
	if err != nil {
		return Header{}, err
	}
	objects, err := exportObjects(manifest)
	if err != nil {
		return Header{}, err
	}
	header := Header{1, binding, digest, unclean, manifest, len(objects)}
	_, err = validateHeader(header)
	return header, err
}

// RelaySnapshot verifies the private Helper stream and withholds its footer until exact EOF.
// The caller owns read cancellation and closes the private stream when authority is revoked.
func RelaySnapshot(ctx context.Context, input io.Reader, output io.Writer, check func(Header, bool) error) error {
	reader := &exportReader{input: input, nextTimeout: ScanTimeout}
	input = reader
	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(input, magic); err != nil || string(magic) != Magic {
		return ErrUnavailable
	}
	var header Header
	if err := readExportFrame(input, &header, maxManifestBytes, "version", "binding", "tree_digest", "unclean", "manifest", "file_objects"); err != nil {
		return err
	}
	objects, err := validateHeader(header)
	if err != nil {
		return err
	}
	if err := check(header, true); err != nil {
		return err
	}
	if _, err := io.WriteString(output, Magic); err != nil {
		return err
	}
	if err := writeFrame(output, header, maxManifestBytes); err != nil {
		return err
	}
	for _, entry := range objects {
		if err := check(header, false); err != nil {
			return err
		}
		if err := copyExportObject(ctx, output, io.LimitReader(input, entry.Size), entry); err != nil {
			return err
		}
		var marker [1]byte
		if _, err := io.ReadFull(input, marker[:]); err != nil || marker[0] != 1 {
			return ErrUnavailable
		}
		if err := check(header, false); err != nil {
			return err
		}
		if _, err := output.Write(marker[:]); err != nil {
			return err
		}
	}
	var footer completion
	reader.nextTimeout = ScanTimeout
	if err := readExportFrame(input, &footer, 4096, "version", "tree_digest", "file_objects", "complete"); err != nil {
		return err
	}
	if footer != (completion{1, header.TreeDigest, len(objects), true}) {
		return ErrUnavailable
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || err != io.EOF {
		return ErrUnavailable
	}
	if err := check(header, true); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeFrame(output, footer, 4096)
}

func validateHeader(header Header) ([]skillmanager.Entry, error) {
	if header.Version != 1 || header.Binding.Validate() != nil {
		return nil, ErrUnavailable
	}
	digest, err := skillmanager.Digest(header.Manifest)
	if err != nil || digest != header.TreeDigest {
		return nil, ErrUnavailable
	}
	objects, err := exportObjects(header.Manifest)
	if err != nil || len(objects) != header.FileObjects {
		return nil, ErrUnavailable
	}
	return objects, nil
}

func copyExportObject(ctx context.Context, output io.Writer, input io.Reader, entry skillmanager.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	verifier, err := skillmanager.NewContentVerifier(entry)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(io.MultiWriter(output, verifier), input, entry.Size); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := input.Read(extra[:]); n != 0 || err != io.EOF {
		return ErrUnavailable
	}
	if err := verifier.Finish(); err != nil {
		return err
	}
	return ctx.Err()
}

func readExportFrame(reader io.Reader, value any, maximum uint32, names ...string) error {
	var length uint32
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil || length == 0 || length > maximum {
		return ErrUnavailable
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ErrUnavailable
	}
	remaining := make(map[string]bool, len(names))
	for _, name := range names {
		remaining[name] = true
	}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || !remaining[name] {
			return ErrUnavailable
		}
		delete(remaining, name)
		var field json.RawMessage
		if decoder.Decode(&field) != nil || bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
			return ErrUnavailable
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(remaining) != 0 {
		return ErrUnavailable
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return ErrUnavailable
	}
	return json.Unmarshal(data, value)
}
