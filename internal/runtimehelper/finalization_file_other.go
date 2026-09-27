//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"os"
)

func (e Engine) openFinalizationFile(context.Context, Request) (*os.File, finalizationFileResponse, error) {
	return nil, finalizationFileResponse{}, errors.New("retained finalization requires Linux")
}
