//go:build linux

package runtimehelper

import (
	"errors"
	"net"
	"syscall"
)

func peerUID(connection net.Conn) (int, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return -1, errors.New("runtime helper requires a Unix connection")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid := -1
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			credentialErr = err
			return
		}
		uid = int(credential.Uid)
	}); err != nil {
		return -1, err
	}
	return uid, credentialErr
}
