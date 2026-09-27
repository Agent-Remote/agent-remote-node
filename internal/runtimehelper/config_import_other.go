//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
)

func (e Engine) importAccountConfig(_ context.Context, _ Request) (map[string]any, error) {
	return nil, errors.New("safe helper configuration import requires Linux")
}
