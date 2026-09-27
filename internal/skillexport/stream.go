// Package skillexport streams an authorized immutable Helper capture through the SSH gateway.
package skillexport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"sort"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const maxManifestBytes = 64 << 20

// MaxExpandedBytes is the manifest's signed byte-count bound, not runtime admission quota.
// Recovery must include retained bytes above the configured capture limit.
const MaxExpandedBytes int64 = math.MaxInt64

// MaxEntries bounds in-memory metadata for both frozen and stopped-work exports.
const MaxEntries = 100000

// Magic identifies the framed, complete-directory frozen export protocol.
const Magic = "ARSKEX\x00\x01"

// ErrUnavailable is the only public gateway failure; grants, Helper paths and private errors stay private.
var ErrUnavailable = errors.New("STATE_EXPORT_UNAVAILABLE: snapshot export did not complete")

// Authority revalidates an original user grant using authenticated Node HTTP.
type Authority interface {
	VerifyNodeExport(context.Context, string, string, string, string, string) (skillmanager.NodeExportPermission, error)
}

// RenewalAuthority can continue a still-live original grant under the same exact read authority.
type RenewalAuthority interface {
	RenewNodeExport(context.Context, string, string, string, string, string) (skillmanager.NodeExportRenewal, error)
}

// FrozenStore reads only immutable Helper captures; it has no preparation, capture or cleanup methods.
type FrozenStore interface {
	HoldSkillFinalization(context.Context, string, skillmanager.SnapshotBinding) (*os.File, skillmanager.FinalizationRecord, error)
	InspectSkillFinalization(context.Context, string, string, string) (skillmanager.FinalizationRecord, error)
	ReadSkillFinalization(context.Context, string, skillmanager.SnapshotBinding) (skillmanager.FinalizationRecord, skillmanager.Manifest, error)
	OpenSkillFinalizationObjects(context.Context, string, skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error)
}

// Identity comes from local Node configuration and installed SSH forced-command arguments.
type Identity struct {
	NodeID     string
	SnapshotID string
	DeviceID   string
	SSHKeyID   string
}

// Header carries complete manifest metadata without grants or private Helper paths.
type Header struct {
	Version     int                            `json:"version"`
	Binding     skillmanager.NodeExportBinding `json:"binding"`
	TreeDigest  string                         `json:"tree_digest"`
	Unclean     bool                           `json:"unclean"`
	Manifest    skillmanager.Manifest          `json:"manifest"`
	FileObjects int                            `json:"file_objects"`
}

type completion struct {
	Version     int    `json:"version"`
	TreeDigest  string `json:"tree_digest"`
	FileObjects int    `json:"file_objects"`
	Complete    bool   `json:"complete"`
}

// Serve owns transport closure and its authorization loop until the complete verified stream ends.
// Success is read-only: it cannot acknowledge upload, publish account state or release local content.
func Serve(parent context.Context, connection io.ReadWriteCloser, identity Identity, authority Authority, store FrozenStore) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	bounded := boundedWriteConnection{connection, NewIdleWriter(ctx, connection, cancel)}
	if err := serve(ctx, bounded, identity, authority, store); err != nil {
		return ErrUnavailable
	}
	return nil
}

func serve(ctx context.Context, connection io.ReadWriteCloser, identity Identity, authority Authority, store FrozenStore) error {
	request, err := readGrant(ctx, connection)
	if err != nil {
		return err
	}
	grant := request.grant
	started := time.Now()
	initial, err := authority.VerifyNodeExport(ctx, identity.NodeID, identity.SnapshotID, identity.DeviceID, identity.SSHKeyID, grant)
	if err != nil || initial.Validate() != nil || !matchesIdentity(initial, identity) || !initial.ExpiresAt.After(time.Now()) {
		return ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	authorization := newExportAuthorization(ctx, cancel, identity, grant, initial, authority, started)
	defer authorization.close()
	reauthorize := authorization.check
	if err := reauthorize(); err != nil {
		return err
	}
	scanCtx, scanCancel := context.WithTimeout(ctx, ScanTimeout)
	defer scanCancel()
	record, err := store.InspectSkillFinalization(scanCtx, "export-inspect", identity.NodeID, initial.Binding.SessionID)
	if err != nil {
		// Helper independently requires ENOENT for the directory itself. Inspection errors
		// never authorize work reads when any frozen capture (including corrupt data) exists.
		if request.recovery {
			recovery, ok := store.(RecoveryStore)
			if !ok || initial.IncomingDigest != nil {
				return ErrUnavailable
			}
			return recovery.StreamStoppedSkillRecovery(ctx, "export-recovery", initial.Binding, connection, authorization.observe)
		}
		recovery, ok := store.(StoppedStore)
		if !ok {
			return ErrUnavailable
		}
		return recovery.StreamStoppedSkillExport(ctx, "export-stopped", initial.Binding, connection, authorization.observe)
	}
	if initial.MatchCapture(record) != nil {
		return ErrUnavailable
	}
	if err := authorization.observe(Header{Binding: initial.Binding, TreeDigest: record.TreeDigest, Unclean: record.Unclean}, false); err != nil {
		return err
	}
	hold, held, err := store.HoldSkillFinalization(scanCtx, "export-hold", record.Binding)
	if err != nil {
		return ErrUnavailable
	}
	if hold == nil {
		return ErrUnavailable
	}
	defer hold.Close()
	if !skillmanager.SameFinalizationInput(held, record) {
		return ErrUnavailable
	}
	loaded, manifest, err := store.ReadSkillFinalization(scanCtx, "export-manifest", record.Binding)
	if err != nil || initial.MatchCapture(loaded) != nil || loaded.TreeDigest != record.TreeDigest || loaded.Unclean != record.Unclean {
		return ErrUnavailable
	}
	digest, err := skillmanager.Digest(manifest)
	if err != nil || digest != record.TreeDigest {
		return ErrUnavailable
	}
	objects, err := exportObjects(manifest)
	if err != nil {
		return err
	}
	if err := scanCtx.Err(); err != nil {
		return err
	}
	scanCancel()
	if err := authorization.refresh(); err != nil {
		return err
	}
	if _, err := io.WriteString(connection, Magic); err != nil {
		return err
	}
	if err := writeFrame(connection, Header{1, initial.Binding, digest, record.Unclean, manifest, len(objects)}, maxManifestBytes); err != nil {
		return err
	}
	reader, err := store.OpenSkillFinalizationObjects(ctx, "export-objects", record)
	if err != nil {
		return err
	}
	if reader == nil {
		return ErrUnavailable
	}
	defer func() { _ = reader.Close() }()
	opened := time.Now()
	for _, entry := range objects {
		if err := reauthorize(); err != nil {
			return err
		}
		if time.Since(opened) >= 10*time.Minute {
			next, err := store.OpenSkillFinalizationObjects(ctx, "export-objects", record)
			if err != nil || next == nil {
				return ErrUnavailable
			}
			// The outer hold and replacement reader protect content before the old reader closes.
			_ = reader.Close()
			reader, opened = next, time.Now()
		}
		if err := sendObject(ctx, connection, reader, entry); err != nil {
			return err
		}
		if err := reauthorize(); err != nil {
			return err
		}
		if _, err := connection.Write([]byte{1}); err != nil {
			return err
		}
	}
	if err := authorization.refresh(); err != nil {
		return err
	}
	return writeFrame(connection, completion{1, digest, len(objects), true}, 4096)
}

func matchesIdentity(permission skillmanager.NodeExportPermission, identity Identity) bool {
	return permission.Binding.NodeID == identity.NodeID && permission.Binding.SnapshotID == identity.SnapshotID &&
		permission.DeviceID == identity.DeviceID && permission.SSHKeyID == identity.SSHKeyID
}

func exportObjects(manifest skillmanager.Manifest) ([]skillmanager.Entry, error) {
	if len(manifest.Entries) > MaxEntries {
		return nil, ErrUnavailable
	}
	objects := make(map[string]skillmanager.Entry)
	var total int64
	for _, entry := range manifest.Entries {
		if entry.Kind != "file" {
			continue
		}
		if entry.Size > MaxExpandedBytes-total || entry.Size < 0 {
			return nil, ErrUnavailable
		}
		total += entry.Size
		if prior, ok := objects[entry.SHA256]; ok && (prior.Size != entry.Size || prior.ContentKind != entry.ContentKind) {
			return nil, ErrUnavailable
		}
		objects[entry.SHA256] = entry
	}
	result := make([]skillmanager.Entry, 0, len(objects))
	for _, entry := range objects {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SHA256 < result[j].SHA256 })
	return result, nil
}

func sendObject(ctx context.Context, writer io.Writer, store skillmanager.FrozenObjectReader, expected skillmanager.Entry) error {
	file, entry, err := store.Open(ctx, expected.SHA256)
	if err != nil {
		return err
	}
	defer file.Close()
	if entry.Kind != "file" || entry.SHA256 != expected.SHA256 || entry.Size != expected.Size || entry.ContentKind != expected.ContentKind {
		return ErrUnavailable
	}
	verifier, err := skillmanager.NewContentVerifier(expected)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(io.MultiWriter(writer, verifier), file, entry.Size); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := file.Read(extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return ErrUnavailable
	}
	if err := verifier.Finish(); err != nil {
		return err
	}
	return ctx.Err()
}

func writeFrame(writer io.Writer, value any, maximum int) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > maximum {
		return ErrUnavailable
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = io.Copy(writer, bytes.NewReader(data))
	return err
}
