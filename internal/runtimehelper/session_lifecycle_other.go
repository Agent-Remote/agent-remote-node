//go:build !linux

package runtimehelper

import "context"

func (e Engine) validateManagedStopIntent(_ Request, _ sessionLifecycleIdentity) error { return nil }

func (e Engine) lockSessionLifecycle(_ context.Context, _, _ string) (func(), error) {
	return func() {}, nil
}
func (e Engine) publishSessionStop(_ context.Context, _ sessionStopIntent) error {
	return errSessionStopPending
}
func (e Engine) sessionStopState(_ context.Context, _ string) (bool, error) { return false, nil }
func (e Engine) requireSessionOpen(_ context.Context, _ string) error       { return nil }
func (e Engine) submitSessionLaunch(_ context.Context, _, _ string, submit func() error) error {
	return submit()
}
func (e Engine) probeSessionLifecycle(_ context.Context) bool { return false }

func (e Engine) withSessionAdmission(_ context.Context, _, _ string, submit func() error) error {
	return submit()
}
