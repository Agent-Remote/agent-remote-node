//go:build !linux && !darwin

package egobrowser

import (
	"errors"
	"net"
)

func peerUID(_ *net.UnixConn) (uint32, error) {
	return 0, errors.New("peer credentials are unavailable on this platform")
}
