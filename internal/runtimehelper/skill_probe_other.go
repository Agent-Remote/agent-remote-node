//go:build !linux

package runtimehelper

import "errors"

func (e Engine) probeSkillStorage() error {
	return errors.New("managed skill runtime requires Linux")
}
