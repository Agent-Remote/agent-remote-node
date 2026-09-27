package skillmanager

import (
	"context"
	"os"
)

// FrozenObjectReader is a sequential reader for one previously validated frozen capture.
// Close ends its read lifetime; callers must verify bytes and close each returned file.
type FrozenObjectReader interface {
	Open(context.Context, string) (*os.File, Entry, error)
	Close() error
}
