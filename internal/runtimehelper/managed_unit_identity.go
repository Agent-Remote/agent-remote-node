package runtimehelper

import (
	"errors"
	"strings"
)

func requireRetainedInvocation(state nativeUnitState, spec SessionSpec, invocation string) error {
	if state.LoadState == "not-found" {
		if state.InvocationID != "" && state.InvocationID != strings.Repeat("0", 32) || state.User != "" || state.Transient != "" && state.Transient != "no" {
			return errors.New("absent unit contradicts retained runtime inspection")
		}
		return nil
	}
	if !validManagedInvocationID(invocation) || state.InvocationID != invocation || state.Transient != "yes" || state.User != spec.Username ||
		state.ControlGroup != "" && state.ControlGroup != "/system.slice/"+spec.UnitName ||
		state.ActiveState == "active" && state.SubState == "running" && state.ControlGroup == "" {
		return errors.New("current unit is not the original managed invocation")
	}
	return nil
}
