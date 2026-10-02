//go:build !linux

package runtimehelper

import (
	"errors"
	"io"
)

// TerminalHost is supported only on the Linux runtime host.
func TerminalHost(io.Reader, io.Writer) error { return errors.New("terminal host requires Linux") }

// TerminalClient is supported only on the Linux runtime host.
func TerminalClient(string) error { return errors.New("terminal client requires Linux") }
