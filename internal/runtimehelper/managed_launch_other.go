//go:build !linux

package runtimehelper

import "context"

func (e Engine) startManagedSession(_ context.Context, _ Request) (map[string]any, error) {
	return nil, errManagedStartPending
}

func (e Engine) recoverManagedSession(_ context.Context, _ Request) (map[string]any, error) {
	return nil, errManagedStartPending
}
