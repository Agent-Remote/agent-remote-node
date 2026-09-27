//go:build !linux

package runtimehelper

import "errors"

func (e Engine) checkAccountRuntimeFence(_, _ string) error {
	return errors.New("account skill fence verification requires the Linux helper")
}
