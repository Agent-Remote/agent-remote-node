//go:build linux

package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func dialNetworkNamespaceLoopback(ctx context.Context, namespace string, port int) (connection *net.TCPConn, err error) {
	if namespace == "" || validateName(namespace, "network_namespace") != nil {
		return nil, errors.New("managed network namespace is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	shouldUnlock := true
	defer func() {
		if shouldUnlock {
			runtime.UnlockOSThread()
		}
	}()
	threadNamespacePath := "/proc/self/task/" + strconv.Itoa(unix.Gettid()) + "/ns/net"
	originalNamespace, err := os.Open(threadNamespacePath)
	if err != nil {
		return nil, fmt.Errorf("open current network namespace: %w", err)
	}
	defer originalNamespace.Close()
	targetNamespace, err := os.Open("/run/netns/" + namespace)
	if err != nil {
		return nil, fmt.Errorf("open managed network namespace: %w", err)
	}
	defer targetNamespace.Close()
	if err := unix.Setns(int(targetNamespace.Fd()), unix.CLONE_NEWNET); err != nil {
		return nil, fmt.Errorf("enter managed network namespace: %w", err)
	}
	defer func() {
		if restoreErr := unix.Setns(int(originalNamespace.Fd()), unix.CLONE_NEWNET); restoreErr != nil {
			// A thread that cannot restore its namespace must never return to the Go scheduler.
			shouldUnlock = false
			if connection != nil {
				_ = connection.Close()
				connection = nil
			}
			err = fmt.Errorf("restore network namespace: %w", restoreErr)
		}
	}()
	dialer := net.Dialer{}
	for _, address := range []string{
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		net.JoinHostPort("::1", strconv.Itoa(port)),
	} {
		dialedConnection, dialErr := dialer.DialContext(ctx, "tcp", address)
		if dialErr != nil {
			if errors.Is(dialErr, syscall.ECONNREFUSED) {
				err = dialErr
				continue
			}
			err = dialErr
			continue
		}
		tcpConnection, ok := dialedConnection.(*net.TCPConn)
		if !ok {
			dialedConnection.Close()
			return nil, errors.New("runtime loopback connection is not TCP")
		}
		connection = tcpConnection
		return connection, nil
	}
	return nil, fmt.Errorf("connect managed session loopback: %w", err)
}
