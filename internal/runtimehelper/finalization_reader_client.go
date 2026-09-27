package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type finalizationObjects struct {
	connection *net.UnixConn
	manifest   *os.File
	capture    skillmanager.FinalizationRecord
	ctx        context.Context
	cancel     context.CancelFunc
	stop       func() bool
	once       sync.Once
}

// OpenSkillFinalizationObjects opens a bounded reader pinned to one exact frozen capture.
// The returned reader must remain open until all object consumers finish.
func (c Client) OpenSkillFinalizationObjects(parent context.Context, requestID string, capture skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error) {
	if validateID(requestID, "request_id") != nil || capture.Validate() != nil || capture.ObjectsVersion != 1 {
		return nil, errors.New("invalid frozen reader identity")
	}
	ctx, cancel := context.WithTimeout(parent, finalizationReaderTimeout)
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		cancel()
		return nil, err
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		cancel()
		return nil, errors.New("frozen reader requires Unix socket")
	}
	reader := &finalizationObjects{connection: unixConnection, capture: capture, ctx: ctx, cancel: cancel}
	reader.stop = context.AfterFunc(ctx, func() { _ = connection.Close() })
	ready := false
	defer func() {
		if !ready {
			_ = reader.Close()
		}
	}()
	deadline, _ := ctx.Deadline()
	_ = connection.SetDeadline(deadline)
	payload, err := Map(finalizationReaderRequest{Capture: capture})
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(connection).Encode(Request{Version: ProtocolVersion, RequestID: requestID, Operation: finalizationReaderOperation, Payload: payload}); err != nil {
		return nil, err
	}
	file, metadata, err := reader.receive(finalizationFileRequest{Binding: capture.Binding, Kind: "manifest"})
	if err != nil {
		return nil, err
	}
	reader.manifest = file
	data, err := io.ReadAll(io.LimitReader(file, maxCaptureManifestBytes+1))
	if err != nil || int64(len(data)) != metadata.Size || len(data) > maxCaptureManifestBytes {
		return nil, errors.New("reader manifest length changed")
	}
	manifest, err := skillmanager.DecodeManifest(data)
	if err != nil {
		return nil, err
	}
	digest, err := skillmanager.Digest(manifest)
	if err != nil || digest != capture.TreeDigest {
		return nil, errors.New("reader manifest identity changed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ready = true
	return reader, nil
}

func (r *finalizationObjects) receive(expected finalizationFileRequest) (*os.File, finalizationFileResponse, error) {
	var metadata finalizationFileResponse
	file, err := receiveRetainedFile(r.connection, func(data []byte, count int) (int64, error) {
		var err error
		metadata, err = decodeFinalizationFileFrame(data, count, expected)
		if err == nil && !skillmanager.SameFinalizationInput(metadata.Record, r.capture) {
			err = errors.New("reader response changed original capture")
		}
		return metadata.Size, err
	})
	if err != nil {
		return nil, metadata, err
	}
	if _, err := r.connection.Write([]byte{fileDescriptorTransferAck}); err != nil {
		_ = file.Close()
		return nil, metadata, err
	}
	return file, metadata, nil
}

func (r *finalizationObjects) Open(ctx context.Context, digest string) (*os.File, skillmanager.Entry, error) {
	if err := errors.Join(ctx.Err(), r.ctx.Err()); err != nil {
		return nil, skillmanager.Entry{}, err
	}
	payload := finalizationFileRequest{Binding: r.capture.Binding, Kind: "object", Digest: digest, TreeDigest: r.capture.TreeDigest, Unclean: &r.capture.Unclean}
	if err := payload.validate(); err != nil {
		return nil, skillmanager.Entry{}, err
	}
	deadline, _ := r.ctx.Deadline()
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = r.connection.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = r.connection.Close() })
	defer stop()
	if err := json.NewEncoder(r.connection).Encode(payload); err != nil {
		_ = r.Close()
		return nil, skillmanager.Entry{}, err
	}
	file, metadata, err := r.receive(payload)
	if err != nil {
		_ = r.Close()
		return nil, skillmanager.Entry{}, err
	}
	if err := errors.Join(ctx.Err(), r.ctx.Err()); err != nil {
		_ = file.Close()
		_ = r.Close()
		return nil, skillmanager.Entry{}, err
	}
	return file, *metadata.Entry, nil
}

func (r *finalizationObjects) Close() error {
	var err error
	r.once.Do(func() {
		r.stop()
		r.cancel()
		err = r.connection.Close()
		if r.manifest != nil {
			err = errors.Join(err, r.manifest.Close())
		}
	})
	return err
}
