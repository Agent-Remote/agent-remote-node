//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
)

func (e Engine) prepareManagedSessionSpec(_ context.Context, _ Request) (map[string]any, error) {
	return nil, errors.New("managed session spec creation requires Linux")
}

func (e Engine) saveManagedSpec(_ SessionSpec) error {
	return errors.New("managed session spec publication requires Linux")
}

func (e Engine) requireManagedSpecReceipt(_ SessionSpec) error {
	return errors.New("managed session spec receipts require Linux")
}
