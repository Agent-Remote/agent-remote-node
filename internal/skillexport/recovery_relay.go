package skillexport

import (
	"context"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// RelayRecovery verifies bounded metadata, every file and the complete ordered recovery digest.
// The footer is withheld until exact Helper EOF and fresh original authorization. This relay
// cannot attest source topology; the privileged source and destination independently validate it.
func RelayRecovery(ctx context.Context, input io.Reader, output io.Writer, check func(Header, bool) error) error {
	reader := &exportReader{input: input, nextTimeout: ScanTimeout}
	magic := make([]byte, len(RecoveryMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != RecoveryMagic {
		return ErrUnavailable
	}
	var header RecoveryHeader
	if err := readExportFrame(reader, &header, 4096, "version", "binding", "recovery_digest", "unclean", "entries", "file_bytes", "file_objects"); err != nil {
		return err
	}
	if header.validate() != nil || check == nil {
		return ErrUnavailable
	}
	if err := check(header.observation(), true); err != nil {
		return err
	}
	if _, err := io.WriteString(output, RecoveryMagic); err != nil {
		return err
	}
	if err := writeFrame(output, header, 4096); err != nil {
		return err
	}
	hasher := skillmanager.NewRecoveryHasher()
	for index := int64(0); index < header.Entries; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := check(header.observation(), false); err != nil {
			return err
		}
		var entry skillmanager.Entry
		if err := readExportFrame(reader, &entry, recoveryEntryBytes, "path", "kind", "mode", "size", "sha256", "target", "content_kind", "dependency"); err != nil {
			return err
		}
		seen := hasher.Observation()
		if skillmanager.ValidateRecoveryStart(entry) != nil || entry.Size > header.FileBytes-seen.FileBytes || entry.Kind == "file" && seen.FileObjects >= header.FileObjects {
			return ErrUnavailable
		}
		if err := writeFrame(output, entry, recoveryEntryBytes); err != nil {
			return err
		}
		if entry.Kind == "file" {
			var err error
			entry, err = relayRecoveryFile(reader, output, entry)
			if err != nil {
				return err
			}
		}
		if err := hasher.Add(entry); err != nil {
			return err
		}
		if err := check(header.observation(), false); err != nil {
			return err
		}
	}
	if !header.matches(hasher.Observation()) {
		return ErrUnavailable
	}
	reader.nextTimeout = ScanTimeout
	var footer recoveryCompletion
	if err := readExportFrame(reader, &footer, 4096, "version", "recovery_digest", "entries", "file_bytes", "file_objects", "complete"); err != nil {
		return err
	}
	if footer != header.completion() {
		return ErrUnavailable
	}
	var extra [1]byte
	if n, err := reader.Read(extra[:]); n != 0 || err != io.EOF {
		return ErrUnavailable
	}
	if err := check(header.observation(), true); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeFrame(output, footer, 4096)
}

func relayRecoveryFile(input io.Reader, output io.Writer, entry skillmanager.Entry) (skillmanager.Entry, error) {
	verifier, err := skillmanager.NewRecoveryFileVerifier(entry)
	if err != nil {
		return skillmanager.Entry{}, err
	}
	if _, err := io.CopyN(io.MultiWriter(verifier, output), input, entry.Size); err != nil {
		return skillmanager.Entry{}, err
	}
	var content recoveryContent
	if err := readExportFrame(input, &content, 4096, "sha256", "content_kind"); err != nil {
		return skillmanager.Entry{}, err
	}
	entry, err = verifier.Complete(content.SHA256, content.ContentKind)
	if err != nil {
		return skillmanager.Entry{}, err
	}
	if err := writeFrame(output, content, 4096); err != nil {
		return skillmanager.Entry{}, err
	}
	return entry, nil
}
