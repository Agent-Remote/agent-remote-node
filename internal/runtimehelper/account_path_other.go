//go:build !linux && !darwin

package runtimehelper

import "errors"

func (e Engine) checkLegacyAccountPath(_, _ string) error {
	return errors.New("safe runtime account paths are unsupported on this platform")
}
