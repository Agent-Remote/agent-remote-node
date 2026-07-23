//go:build !linux

package runtimehelper

import "net"

func peerUID(connection net.Conn) (int, error) {
	_ = connection
	return -1, nil
}
