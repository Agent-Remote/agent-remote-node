package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const finalizationReaderOperation = "read_skill_finalization_objects"
const finalizationReaderTimeout = 15 * time.Minute

type finalizationReaderRequest struct {
	Capture skillmanager.FinalizationRecord `json:"capture"`
}

func (r *finalizationReaderRequest) UnmarshalJSON(data []byte) error {
	type plain finalizationReaderRequest
	return decodeFinalizationFileFields(data, (*plain)(r), "", "capture")
}

type retainedObjectReader interface {
	Open(string) (*os.File, skillmanager.Entry, error)
	Close() error
}

func (s *Server) handleFinalizationReader(parent context.Context, connection net.Conn, input *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, finalizationReaderTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	// A pipe owns socket reads so EOF can cancel initial validation and lock waiting too.
	withFinalizationReaderInput(cancel, connection, input, func(input *bufio.Reader) {
		if err := s.lockSkillPreparation(ctx); err != nil {
			return
		}
		objects, manifest, record, err := s.engine.openFinalizationReader(ctx, request)
		s.mu.Unlock()
		if err != nil {
			_ = json.NewEncoder(connection).Encode(errorResponse("FINALIZATION_UNAVAILABLE", "Retained skill finalization is unavailable."))
			return
		}
		defer objects.Close()
		if err := sendReaderFile(ctx, connection, input, manifest, record, "manifest", nil); err != nil {
			return
		}
		for count := 0; count < 100_000; count++ {
			line, err := readBoundedLine(input, maxHelperRequestBytes)
			if err != nil || ctx.Err() != nil {
				return
			}
			var requested finalizationFileRequest
			if err := decodeStrictJSON(line, &requested); err != nil || requested.validate() != nil || requested.Kind != "object" || requested.Binding != record.Binding || requested.TreeDigest != record.TreeDigest || requested.Unclean == nil || *requested.Unclean != record.Unclean {
				return
			}
			file, entry, err := objects.Open(requested.Digest)
			if err != nil {
				return
			}
			err = sendReaderFile(ctx, connection, input, file, record, "object", &entry)
			_ = file.Close()
			if err != nil {
				return
			}
		}
	})
}

func sendReaderFile(ctx context.Context, connection net.Conn, input *bufio.Reader, file *os.File, record skillmanager.FinalizationRecord, kind string, entry *skillmanager.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	metadata, err := finalizationFileMetadata(file, record, kind, entry)
	if err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Version int                      `json:"version"`
		OK      bool                     `json:"ok"`
		Result  finalizationFileResponse `json:"result"`
	}{ProtocolVersion, true, metadata})
	if err != nil || len(data)+1 > maxCaptureFileResponseBytes {
		return errors.New("invalid reader descriptor metadata")
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("reader requires Unix socket")
	}
	deadline := time.Now().Add(fileDescriptorAckTimeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = connection.SetWriteDeadline(deadline)
	data = append(data, '\n')
	n, _, err := unixConnection.WriteMsgUnix(data, syscall.UnixRights(int(file.Fd())), nil)
	if err != nil || n != len(data) {
		return errors.New("reader descriptor transfer failed")
	}
	// The input pump has the sole socket read; its read deadline bounds acknowledgements.
	_ = connection.SetReadDeadline(deadline)
	ack, err := input.ReadByte()
	_ = connection.SetReadDeadline(time.Time{})
	if err != nil || ack != fileDescriptorTransferAck {
		return errors.New("reader descriptor acknowledgement failed")
	}
	return ctx.Err()
}
