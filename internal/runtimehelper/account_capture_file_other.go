//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"net"
	"os"
)

func readCaptureMessage(_ *net.UnixConn, _, _ []byte) (int, int, int, error) {
	return 0, 0, 0, errors.New("retained account capture requires Linux")
}

func (e Engine) openAccountCaptureFile(_ context.Context, _ Request) (*os.File, accountCaptureFileResponse, error) {
	return nil, accountCaptureFileResponse{}, errors.New("retained account capture requires Linux")
}

func validateCaptureFileDescriptor(_ *os.File, _ int64) error {
	return errors.New("retained account capture requires Linux")
}
