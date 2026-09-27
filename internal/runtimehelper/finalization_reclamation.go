package runtimehelper

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const finalizationReclamationOperation = "reclaim_skill_finalization"
const finalizationReclamationResumeOperation = "resume_skill_reclamation"
const finalizationReclamationTimeout = 15 * time.Minute
const maxReclamationFrameBytes = 16 << 10

var errReclamationPending = errors.New("retained skill reclamation requires recovery")

// The deadline originates in the Helper process and never appears in a socket frame.
type finalizationReclamationAuthority func(context.Context, skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error)

type finalizationReclamationFrame struct {
	Version         int                                       `json:"version"`
	Kind            string                                    `json:"kind"`
	Challenge       string                                    `json:"challenge,omitempty"`
	Acknowledgement *skillmanager.FinalizationAcknowledgement `json:"acknowledgement,omitempty"`
	Record          *skillmanager.FinalizationRecord          `json:"record,omitempty"`
}

func readReclamationFrame(reader *bufio.Reader) (finalizationReclamationFrame, error) {
	var frame finalizationReclamationFrame
	data, err := readBoundedLine(reader, maxReclamationFrameBytes)
	if err != nil {
		return frame, errReclamationPending
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		return frame, errReclamationPending
	}
	fields := []string{"version", "kind"}
	switch frame.Kind {
	case "authorize":
		fields = append(fields, "challenge", "acknowledgement")
	case "reclaimed":
		fields = append(fields, "record")
	case "pending":
	default:
		return frame, errReclamationPending
	}
	if decodeFinalizationFileFields(data, &frame, "", fields...) != nil || frame.Version != ProtocolVersion {
		return frame, errReclamationPending
	}
	return frame, nil
}

func (s *Server) handleFinalizationReclamation(parent context.Context, connection net.Conn, reader *bufio.Reader, request Request) {
	ctx, cancel := context.WithTimeout(parent, finalizationReclamationTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	frames := make(chan []byte, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if request.Operation == finalizationReclamationOperation {
			data, err := readBoundedLine(reader, maxReclamationFrameBytes)
			if err != nil {
				cancel()
				return
			}
			frames <- data
		}
		// A second input, or EOF, cancels work even after the authorization frame was consumed.
		_, _ = reader.ReadByte()
		cancel()
	}()
	defer func() { _ = connection.Close(); <-done }()
	encoder := json.NewEncoder(connection)
	var capture skillmanager.FinalizationRecord
	var err error
	if request.Version != ProtocolVersion || validateID(request.RequestID, "request_id") != nil {
		err = errReclamationPending
	} else {
		switch request.Operation {
		case finalizationReclamationOperation:
			var input finalizationCleanupRequest
			err = decodeStrictPayload(request.Payload, &input)
			capture = input.Capture
			if err == nil {
				err = s.reclaimFinalization(ctx, capture, func(ctx context.Context, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error) {
					return requestReclamationAuthority(ctx, connection, encoder, frames, ack)
				})
			}
		case finalizationReclamationResumeOperation:
			var input skillReconciliationRequest
			err = decodeStrictPayload(request.Payload, &input)
			if err == nil {
				capture, err = s.resumeFinalizationReclamation(ctx, input.NodeID, input.SessionID)
			}
		default:
			err = errReclamationPending
		}
	}
	frame := finalizationReclamationFrame{Version: ProtocolVersion, Kind: "pending"}
	if err == nil && ctx.Err() == nil {
		frame.Kind, frame.Record = "reclaimed", &capture
	}
	_ = encoder.Encode(frame)
}

func requestReclamationAuthority(parent context.Context, connection net.Conn, encoder *json.Encoder, frames <-chan []byte, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, time.Time, error) {
	var authority skillmanager.ReclamationAuthorization
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return authority, time.Time{}, err
	}
	nonce[6], nonce[8] = nonce[6]&0x0f|0x40, nonce[8]&0x3f|0x80
	challenge := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:])
	// Start before writing the challenge. A slow peer, HTTP scan and both IPC directions consume
	// this one budget; neither Server wall time nor a worker-supplied duration can extend it.
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if err := encoder.Encode(finalizationReclamationFrame{Version: ProtocolVersion, Kind: "authorize", Challenge: challenge, Acknowledgement: &ack}); err != nil {
		return authority, time.Time{}, err
	}
	select {
	case data := <-frames:
		if json.Unmarshal(data, &authority) != nil || authority.RequestID != challenge || authority.Match(ack) != nil {
			return authority, time.Time{}, errReclamationPending
		}
	case <-ctx.Done():
		return authority, time.Time{}, ctx.Err()
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return authority, time.Time{}, errReclamationPending
	}
	return authority, deadline, nil
}
