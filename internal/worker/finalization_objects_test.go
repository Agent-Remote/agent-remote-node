package worker

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type rotatingFinalizationFixture struct {
	*finalizationFixture
	readers []*trackedFinalizationReader
	fail    bool
	now     time.Time
}

type trackedFinalizationReader struct {
	skillmanager.FrozenObjectReader
	fixture *rotatingFinalizationFixture
	opened  time.Time
	closed  bool
}

func (f *rotatingFinalizationFixture) OpenSkillFinalizationObjects(ctx context.Context, _ string, capture skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if capture != f.capture {
		f.t.Fatal("replacement reader changed capture")
	}
	if len(f.readers) > 0 && f.readers[len(f.readers)-1].closed {
		f.t.Fatal("replacement had a gap in manifest hold ownership")
	}
	if f.fail {
		return nil, errors.New("original reader unavailable")
	}
	reader := &trackedFinalizationReader{FrozenObjectReader: &fixtureFrozenObjects{f.finalizationFixture, capture}, fixture: f, opened: f.now}
	f.readers = append(f.readers, reader)
	return reader, nil
}

func (r *trackedFinalizationReader) Open(ctx context.Context, digest string) (*os.File, skillmanager.Entry, error) {
	if r.closed || r.fixture.now.Sub(r.opened) >= 15*time.Minute {
		return nil, skillmanager.Entry{}, errors.New("old reader cannot serve objects")
	}
	return r.FrozenObjectReader.Open(ctx, digest)
}

func (r *trackedFinalizationReader) Close() error {
	r.closed = true
	return r.FrozenObjectReader.Close()
}

func TestFinalizationObjectsRotateAcrossLongUploadsWithoutDroppingHold(t *testing.T) {
	f := &rotatingFinalizationFixture{finalizationFixture: newFinalizationFixture(t, false, "published"), now: time.Now()}
	source, err := openFinalizationObjectSource(context.Background(), f, f.capture, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, elapsed := range []time.Duration{0, 9 * time.Minute, 2 * time.Minute, 20 * time.Minute} {
		f.now = f.now.Add(elapsed)
		file, entry, err := source.Open(context.Background(), f.manifest.Entries[0].SHA256)
		if err != nil || entry != f.manifest.Entries[0] {
			t.Fatal("long upload lost original frozen bytes", err)
		}
		_ = file.Close()
	}
	if len(f.readers) != 3 || !f.readers[0].closed || !f.readers[1].closed || f.readers[2].closed {
		t.Fatal("reader replacement leaked or prematurely released its hold")
	}
	_ = source.Close()
	if !f.readers[2].closed {
		t.Fatal("final reader was not closed")
	}
	if file, _, err := source.Open(context.Background(), f.manifest.Entries[0].SHA256); err == nil || file != nil {
		t.Fatal("closed source reopened input")
	}
}

func TestFinalizationObjectsRetainOldHoldWhenReplacementFails(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "cancelled"}[cancelled], func(t *testing.T) {
			f := &rotatingFinalizationFixture{finalizationFixture: newFinalizationFixture(t, false, "published"), now: time.Now()}
			source, err := openFinalizationObjectSource(context.Background(), f, f.capture, func() time.Time { return f.now })
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			f.now = f.now.Add(20 * time.Minute)
			f.fail = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			if file, _, err := source.Open(ctx, f.manifest.Entries[0].SHA256); err == nil || file != nil {
				t.Fatal("failed replacement served an object")
			}
			if len(f.readers) != 1 || f.readers[0].closed {
				t.Fatal("failed replacement released original hold")
			}
			_ = source.Close()
			if !f.readers[0].closed {
				t.Fatal("failed upload leaked original hold")
			}
		})
	}
}
