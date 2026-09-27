//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
)

func (e Engine) reconcileSkillSession(_ context.Context, _ Request) (SkillSessionObservation, error) {
	return SkillSessionObservation{}, errors.New("skill lifecycle reconciliation requires Linux")
}
