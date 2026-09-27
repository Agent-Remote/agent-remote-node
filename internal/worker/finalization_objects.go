package worker

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Renew between files before the Helper's 15-minute connection budget expires.
// An already transferred descriptor and the outer manifest hold survive socket expiry.
const finalizationObjectReaderRefresh = 10 * time.Minute

type finalizationObjectSource struct {
	helper  finalizationReader
	capture skillmanager.FinalizationRecord
	reader  skillmanager.FrozenObjectReader
	opened  time.Time
	now     func() time.Time
}

func openFinalizationObjectSource(ctx context.Context, helper finalizationReader, capture skillmanager.FinalizationRecord, now func() time.Time) (*finalizationObjectSource, error) {
	source := &finalizationObjectSource{helper: helper, capture: capture, now: now}
	if err := source.refresh(ctx); err != nil {
		return nil, err
	}
	return source, nil
}

func (s *finalizationObjectSource) refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	started := s.now()
	next, err := s.helper.OpenSkillFinalizationObjects(ctx, "finalization-objects:"+s.capture.Binding.SnapshotID, s.capture)
	if err != nil {
		return err
	}
	if next == nil {
		return errors.New("finalization reader is unavailable")
	}
	// Acquire the next exact-input hold before releasing this connection's hold.
	if s.reader != nil {
		_ = s.reader.Close()
	}
	s.reader, s.opened = next, started
	return nil
}

// Open preserves the original capture while renewing only its bounded Helper connection.
func (s *finalizationObjectSource) Open(ctx context.Context, digest string) (*os.File, skillmanager.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, skillmanager.Entry{}, err
	}
	if s.reader == nil {
		return nil, skillmanager.Entry{}, errors.New("finalization object source is closed")
	}
	if s.now().Sub(s.opened) >= finalizationObjectReaderRefresh {
		if err := s.refresh(ctx); err != nil {
			return nil, skillmanager.Entry{}, err
		}
	}
	return s.reader.Open(ctx, digest)
}

// Close releases the current reader hold without changing the retained transfer.
func (s *finalizationObjectSource) Close() error {
	if s.reader == nil {
		return nil
	}
	err := s.reader.Close()
	s.reader = nil
	return err
}
