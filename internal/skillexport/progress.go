package skillexport

import (
	"context"
	"io"
	"time"
)

// ScanTimeout bounds metadata inspection and each complete stopped-work verification phase.
const ScanTimeout = 15 * time.Minute

// WriteTimeout bounds one output block even when authorization continues to renew.
const WriteTimeout = 30 * time.Second

// NewIdleWriter limits writes to 32 KiB blocks with independent progress deadlines.
// cancel must cancel the owner context and close its transport, including a blocked Write.
func NewIdleWriter(ctx context.Context, output io.Writer, cancel context.CancelFunc) io.Writer {
	return &idleWriter{ctx: ctx, output: output, cancel: cancel}
}

type idleWriter struct {
	ctx    context.Context
	output io.Writer
	cancel context.CancelFunc
}

func (w *idleWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		if err := w.ctx.Err(); err != nil {
			return written, err
		}
		block := data[:min(len(data), 32<<10)]
		timer := time.AfterFunc(WriteTimeout, w.cancel)
		n, err := w.output.Write(block)
		timer.Stop()
		if n < 0 || n > len(block) {
			w.cancel()
			return written, io.ErrShortWrite
		}
		written += n
		if err == nil {
			err = w.ctx.Err()
		}
		if err == nil && n != len(block) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.cancel()
			return written, err
		}
		data = data[n:]
	}
	return written, w.ctx.Err()
}

type boundedWriteConnection struct {
	io.ReadWriteCloser
	output io.Writer
}

func (c boundedWriteConnection) Write(data []byte) (int, error) { return c.output.Write(data) }

type exportReader struct {
	input       io.Reader
	nextTimeout time.Duration
}

func (r *exportReader) Read(data []byte) (int, error) {
	if connection, ok := r.input.(interface{ SetReadDeadline(time.Time) error }); ok {
		timeout := WriteTimeout
		if r.nextTimeout != 0 {
			timeout = r.nextTimeout
		}
		if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return 0, err
		}
	}
	n, err := r.input.Read(data)
	if n > 0 {
		r.nextTimeout = 0
	}
	return n, err
}
