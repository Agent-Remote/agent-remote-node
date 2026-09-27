//go:build !linux && !darwin

package toolaccounts

import "errors"

func writeImportBatch(_ string, _ []preparedImportFile, _ *ImportOwnership) error {
	return errors.New("safe configuration import is unavailable on this platform")
}
