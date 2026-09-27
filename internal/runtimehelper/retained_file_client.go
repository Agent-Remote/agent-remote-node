package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

func (c Client) openRetainedFile(ctx context.Context, request Request, validate func([]byte, int) (int64, error)) (*os.File, error) {
	dialer := net.Dialer{Timeout: c.timeout}
	connection, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline := time.Now().Add(c.timeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nil, err
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return nil, errors.New("capture transfer requires Unix socket")
	}
	file, err := receiveRetainedFile(unixConnection, validate)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, err
	}
	if _, err := connection.Write([]byte{fileDescriptorTransferAck}); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func receiveRetainedFile(connection *net.UnixConn, validate func([]byte, int) (int64, error)) (*os.File, error) {
	data := make([]byte, 0, maxCaptureFileResponseBytes)
	buffer := make([]byte, 4096)
	oob := make([]byte, syscall.CmsgSpace(4*16))
	descriptors := []int{}
	defer func() { closeFileDescriptors(descriptors) }()
	for {
		n, ancillary, flags, err := readCaptureMessage(connection, buffer, oob)
		received, parseErr := parseFileDescriptors(oob[:ancillary])
		descriptors = append(descriptors, received...)
		if parseErr != nil {
			return nil, parseErr
		}
		if err != nil {
			return nil, err
		}
		if n == 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 || len(descriptors) > 1 || len(data)+n > maxCaptureFileResponseBytes {
			return nil, errors.New("invalid capture descriptor frame")
		}
		data = append(data, buffer[:n]...)
		if bytes.IndexByte(data, '\n') >= 0 {
			break
		}
	}

	size, err := validate(data, len(descriptors))
	if err != nil {
		return nil, err
	}
	if len(descriptors) != 1 {
		return nil, errors.New("retained file lacks its unique descriptor")
	}
	file := os.NewFile(uintptr(descriptors[0]), "retained-skill-file")
	if file == nil {
		return nil, errors.New("invalid retained file descriptor")
	}
	descriptors = nil
	if err := validateCaptureFileDescriptor(file, size); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
