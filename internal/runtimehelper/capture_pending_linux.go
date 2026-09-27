package runtimehelper

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// A capture error alone never proves exit. Recheck independent original writer evidence and
// require the existing durable termination record; ENOSPC before that record stays unconfirmed.
func (e Engine) capturePendingObservation(ctx context.Context, store, bundle *os.Root, session skillmanager.SessionSnapshot, captureErr error) (SkillSessionObservation, error) {
	result := SkillSessionObservation{SessionID: session.Snapshot.Binding.SessionID}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := bundle.Lstat("finalization"); !errors.Is(err, os.ErrNotExist) {
		return result, captureErr
	}
	verify, err := e.stoppedExportWriters(ctx, store, bundle, session)
	if err != nil {
		return result, err
	}
	termination, err := skillmanager.ReadTermination(bundle, session.Snapshot.Binding)
	if err != nil {
		return result, err
	}
	if err := verify(ctx); err != nil {
		return result, err
	}
	code := "capture_failed"
	for _, known := range []string{"quota_exceeded", "insufficient_storage", "portability_error"} {
		if strings.HasPrefix(captureErr.Error(), known+":") {
			code = known
			break
		}
	}
	if errors.Is(captureErr, syscall.ENOSPC) {
		code = "insufficient_storage"
	}
	failure := skillmanager.CaptureFailure{Binding: termination.Binding, Unclean: termination.Unclean, Code: code}
	if err := failure.Validate(); err != nil {
		return result, err
	}
	result.State, result.Pending = "capture_pending", &failure
	return result, nil
}
