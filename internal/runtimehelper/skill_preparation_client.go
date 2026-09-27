package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillSnapshotDownloader copies and verifies one authenticated snapshot object into the supplied stream.
// It must honor context cancellation and return success only after the complete source is verified.
type SkillSnapshotDownloader func(context.Context, skillmanager.Entry, io.Writer) error

// PrepareSkillSnapshot streams an original snapshot into Helper-owned atomic preparation.
// The Helper must already hold a matching trusted session spec. Success does not authorize launch.
func (c Client) PrepareSkillSnapshot(ctx context.Context, requestID string, snapshot skillmanager.SkillSnapshot, download SkillSnapshotDownloader) error {
	if validateID(requestID, "request_id") != nil || download == nil {
		return errors.New("invalid skill preparation caller")
	}
	if err := snapshot.Validate(snapshot.SkillSnapshotIdentity); err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > maxSkillPreparationBytes {
		return errors.New("skill preparation input exceeds its limit")
	}
	ctx, cancel := context.WithTimeout(ctx, skillPreparationTimeout)
	defer cancel()
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	header, err := Map(skillPreparationHeader{SessionID: snapshot.SessionID, SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID, InputSize: int64(len(data))})
	if err != nil {
		return err
	}
	if err := json.NewEncoder(connection).Encode(Request{Version: ProtocolVersion, RequestID: requestID, Operation: skillPreparationOperation, Payload: header}); err != nil {
		return err
	}
	reader := bufio.NewReader(connection)
	frame, err := readSkillPreparationFrame(reader)
	if err != nil || !exactSkillFrame(frame, "input") {
		return skillPreparationClientError(ctx)
	}
	if _, err := connection.Write(data); err != nil {
		return skillPreparationClientError(ctx)
	}
	files := skillPreparationFiles(snapshot.Manifest)
	remaining := make(map[string]int)
	for _, entry := range snapshot.Manifest.Entries {
		if entry.Kind == "file" {
			remaining[entry.SHA256]++
		}
	}
	for {
		frame, err := readSkillPreparationFrame(reader)
		if err != nil {
			return skillPreparationClientError(ctx)
		}
		if frame == (skillPreparationFrame{Version: ProtocolVersion, Kind: "prepared", SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID, TreeDigest: snapshot.TreeDigest}) {
			// Exact replay may use an already complete bundle without requesting any objects.
			return ctx.Err()
		}
		entry, found := files[frame.Digest]
		if !found || remaining[frame.Digest] == 0 || frame != (skillPreparationFrame{Version: ProtocolVersion, Kind: "object", Digest: frame.Digest}) {
			return skillPreparationClientError(ctx)
		}
		remaining[frame.Digest]--
		verifier, err := skillmanager.NewContentVerifier(entry)
		if err != nil {
			return err
		}
		// Verification precedes socket writes, so excess bytes cannot become protocol frames.
		writer := io.MultiWriter(verifier, connection)
		if err := download(ctx, entry, writer); err != nil {
			return err
		}
		if err := verifier.Finish(); err != nil {
			return err
		}
		if _, err := connection.Write([]byte{skillObjectComplete}); err != nil {
			return skillPreparationClientError(ctx)
		}
	}
}

func skillPreparationClientError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &Error{Code: "SKILL_PREPARATION_UNAVAILABLE", Message: "The original skill snapshot could not be prepared."}
}
