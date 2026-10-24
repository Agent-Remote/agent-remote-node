//go:build !linux && !darwin

package workspace

import "errors"

// OpenDirectory refuses unsupported runtime hosts.
func OpenDirectory(root string, payload PreparePayload) (*Directory, error) {
	return nil, errors.New("safe workspace preparation is unsupported on this host")
}

// WriteMarker refuses unsupported runtime hosts.
func (d *Directory) WriteMarker(payload PreparePayload) (PrepareResult, error) {
	return PrepareResult{}, errors.New("safe workspace preparation is unsupported on this host")
}
