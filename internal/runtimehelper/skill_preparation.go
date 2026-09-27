package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const skillPreparationOperation = "prepare_skill_snapshot"
const maxSkillPreparationBytes = 64 << 20

// Default-size trees require 100,000 sequential authenticated object transfers. This
// bounds the whole preparation, independently of each HTTP request and renewable task lease.
const skillPreparationTimeout = 3 * time.Hour
const skillObjectComplete = byte(0x06)

type skillPreparationHeader struct {
	SessionID  string `json:"session_id"`
	SnapshotID string `json:"snapshot_id"`
	TaskID     string `json:"task_id"`
	InputSize  int64  `json:"input_size"`
}

type skillPreparationFrame struct {
	Version    int    `json:"version"`
	Kind       string `json:"kind"`
	Digest     string `json:"digest,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	TreeDigest string `json:"tree_digest,omitempty"`
}

func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var data []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > limit-len(data) {
			return nil, errors.New("helper frame exceeds limit")
		}
		data = append(data, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return data, nil
	}
}

func readSkillPreparationFrame(reader *bufio.Reader) (skillPreparationFrame, error) {
	data, err := readBoundedLine(reader, 4096)
	if err != nil {
		return skillPreparationFrame{}, err
	}
	var frame skillPreparationFrame
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return frame, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return frame, err
	}
	if fields["version"] == nil || fields["kind"] == nil {
		return frame, errors.New("missing skill preparation frame identity")
	}
	for key, value := range fields {
		switch key {
		case "version", "kind", "digest", "snapshot_id", "task_id", "tree_digest":
		default:
			return frame, errors.New("unknown skill preparation frame field")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return frame, errors.New("null skill preparation frame field")
		}
	}
	if err := decodeStrictJSON(data, &frame); err != nil {
		return frame, err
	}
	if frame.Version != ProtocolVersion {
		return frame, errors.New("invalid skill preparation protocol")
	}
	return frame, nil
}

func validateSkillPreparationRequest(request Request) (skillPreparationHeader, error) {
	var header skillPreparationHeader
	if request.Version != ProtocolVersion || request.Operation != skillPreparationOperation || validateID(request.RequestID, "request_id") != nil || len(request.Payload) != 4 {
		return header, errors.New("invalid skill preparation request")
	}
	for _, key := range []string{"session_id", "snapshot_id", "task_id", "input_size"} {
		if request.Payload[key] == nil {
			return header, errors.New("missing skill preparation field")
		}
	}
	if err := decodeStrictPayload(request.Payload, &header); err != nil {
		return header, err
	}
	if !validSkillUUID(header.SessionID) || !validSkillUUID(header.SnapshotID) || !validSkillUUID(header.TaskID) || header.InputSize <= 0 || header.InputSize > maxSkillPreparationBytes {
		return header, errors.New("invalid skill preparation binding or size")
	}
	return header, nil
}

func (s *Server) handleSkillPreparation(ctx context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	header, err := validateSkillPreparationRequest(request)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(skillPreparationFrame{Version: ProtocolVersion, Kind: "failed"})
		return
	}
	withSkillPreparationStream(ctx, connection, reader, func(ctx context.Context, input io.Reader) {
		encoder := json.NewEncoder(connection)
		fail := func() { _ = encoder.Encode(skillPreparationFrame{Version: ProtocolVersion, Kind: "failed"}) }
		if err := encoder.Encode(skillPreparationFrame{Version: ProtocolVersion, Kind: "input"}); err != nil {
			return
		}
		data := make([]byte, header.InputSize)
		if _, err := io.ReadFull(input, data); err != nil {
			fail()
			return
		}
		snapshot, err := decodeSkillPreparationInput(data)
		if err != nil || snapshot.SessionID != header.SessionID || snapshot.SnapshotID != header.SnapshotID || snapshot.TaskID != header.TaskID {
			fail()
			return
		}
		opener := &skillPreparationObjects{input: input, encoder: encoder, files: skillPreparationFiles(snapshot.Manifest)}
		if err := s.lockSkillPreparation(ctx); err != nil {
			fail()
			return
		}
		err = s.engine.prepareTransferredSkillSnapshot(ctx, snapshot, opener.open)
		s.mu.Unlock()
		if err != nil || ctx.Err() != nil {
			fail()
			return
		}
		_ = encoder.Encode(skillPreparationFrame{Version: ProtocolVersion, Kind: "prepared", SnapshotID: snapshot.SnapshotID, TaskID: snapshot.TaskID, TreeDigest: snapshot.TreeDigest})
	})
}

// Waiting for a legacy mutation must not keep a disconnected preparation handler alive.
func (s *Server) lockSkillPreparation(ctx context.Context) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// A single reader owns the socket. EOF cancels filesystem work even between object requests.
// The pipe applies backpressure; no whole object or uncontrolled pending data is buffered.
func withSkillPreparationStream(parent context.Context, connection net.Conn, reader io.Reader, run func(context.Context, io.Reader)) {
	ctx, cancel := context.WithTimeout(parent, skillPreparationTimeout)
	defer cancel()
	input, output := io.Pipe()
	stop := context.AfterFunc(ctx, func() {
		_ = connection.Close()
		_ = input.CloseWithError(ctx.Err())
		_ = output.CloseWithError(ctx.Err())
	})
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := io.Copy(output, reader)
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		_ = output.CloseWithError(err)
		cancel()
	}()
	run(ctx, input)
	_ = input.Close()
	_ = connection.Close()
	<-done
}

type skillPreparationObjects struct {
	input   io.Reader
	encoder *json.Encoder
	files   map[string]skillmanager.Entry
}

func skillPreparationFiles(manifest skillmanager.Manifest) map[string]skillmanager.Entry {
	files := make(map[string]skillmanager.Entry)
	for _, entry := range manifest.Entries {
		if entry.Kind == "file" {
			files[entry.SHA256] = entry
		}
	}
	return files
}

func (o *skillPreparationObjects) open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, found := o.files[digest]
	if !found {
		return nil, errors.New("skill preparation requested an unbound object")
	}
	if err := o.encoder.Encode(skillPreparationFrame{Version: ProtocolVersion, Kind: "object", Digest: digest}); err != nil {
		return nil, err
	}
	return &skillPreparationObject{reader: o.input, remaining: entry.Size}, nil
}

type skillPreparationObject struct {
	reader    io.Reader
	remaining int64
	complete  bool
}

func (o *skillPreparationObject) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if o.remaining > 0 {
		if int64(len(buffer)) > o.remaining {
			buffer = buffer[:o.remaining]
		}
		n, err := o.reader.Read(buffer)
		o.remaining -= int64(n)
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return n, err
	}
	if !o.complete {
		var ack [1]byte
		if _, err := io.ReadFull(o.reader, ack[:]); err != nil || ack[0] != skillObjectComplete {
			return 0, errors.New("skill download completion was not verified")
		}
		o.complete = true
	}
	return 0, io.EOF
}

func (o *skillPreparationObject) Close() error { return nil }

func exactSkillFrame(frame skillPreparationFrame, kind string) bool {
	return frame == (skillPreparationFrame{Version: ProtocolVersion, Kind: kind})
}

func decodeSkillPreparationInput(data []byte) (skillmanager.SkillSnapshot, error) {
	var snapshot skillmanager.SkillSnapshot
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return snapshot, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, snapshot.Validate(snapshot.SkillSnapshotIdentity)
}
