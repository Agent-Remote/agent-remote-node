//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
	"net"
)

func dialNetworkNamespaceLoopback(ctx context.Context, namespace string, port int) (*net.TCPConn, error) {
	_ = ctx
	_ = namespace
	_ = port
	return nil, errors.New("session network namespace dialing is unavailable on this platform")
}
