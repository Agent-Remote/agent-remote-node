package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// Observation is separate from readiness: it cannot confirm startup or restore broker admission.
// The durable original intent and Helper's no-relaunch rule bind this first invocation observation.
func (e Engine) observeStartingManagedInvocation(ctx context.Context, store *os.Root, spec SessionSpec, launch skillmanager.SessionLaunch, before nativeUnitState) (skillmanager.SessionLaunch, error) {
	running := before.ActiveState == "active" && before.SubState == "running"
	exited := before.ActiveState == "inactive" || before.ActiveState == "failed" || before.ActiveState == "active" && before.SubState == "exited"
	if before.LoadState != "loaded" || !validManagedInvocationID(before.InvocationID) || !running && !exited {
		return launch, errors.New("starting runtime has no stable invocation")
	}
	if err := requireRetainedInvocation(before, spec, before.InvocationID); err != nil {
		return launch, err
	}
	if running {
		if err := e.requireManagedSpecReceipt(spec); err != nil {
			return launch, err
		}
		if err := e.verifyExistingManagedMount(spec); err != nil {
			return launch, err
		}
	} else {
		if err := verifyRetainedSpecFile(spec); err != nil {
			return launch, err
		}
		if err := confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+spec.UnitName); err != nil {
			return launch, err
		}
	}
	after, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil || before != after || spec.BootID != currentBootID() {
		return launch, errors.New("starting runtime changed during invocation inspection")
	}
	if err := ctx.Err(); err != nil {
		return launch, err
	}
	if err := skillmanager.ObserveSessionLaunch(store, launch, before.InvocationID); err != nil {
		return launch, err
	}
	return skillmanager.ReadSessionLaunch(store, launch)
}
