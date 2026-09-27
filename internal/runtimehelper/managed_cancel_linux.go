package runtimehelper

import (
	"context"
	"time"
)

// A saved launch receipt must not authorize stopping a later service that reused its unit name.
// Starting records have no invocation yet, but still require the exact transient user/cgroup.
func (e Engine) requireManagedCancellationUnit(ctx context.Context, spec SessionSpec, invocation string) error {
	state, err := readNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil {
		return errManagedStartPending
	}
	if state.LoadState == "not-found" || state.ActiveState == "inactive" || state.ActiveState == "failed" {
		return nil
	}
	current, err := e.managedLaunchInvocation(ctx, spec)
	if err != nil || invocation != "" && current != invocation {
		return errManagedStartPending
	}
	return nil
}

func (e Engine) cleanupFailedManagedRecovery(spec SessionSpec, invocation string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := e.requireManagedCancellationUnit(ctx, spec, invocation)
	cancel()
	if err != nil {
		return err
	}
	return e.cleanupFailedNativeLaunch(spec)
}
