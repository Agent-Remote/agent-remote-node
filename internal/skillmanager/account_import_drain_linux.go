package skillmanager

import (
	"errors"
	"io"
	"os"
	"strings"
)

// CheckAccountImportsDrained rejects retained write intents that lack a durable terminal outcome.
// The caller must hold the same Helper serialization as imports and close the account fence first.
func CheckAccountImportsDrained(store *os.Root, nodeID, userID, accountID string) error {
	if _, err := ReadAccountFence(store, nodeID, userID, accountID); err != nil {
		return err
	}
	directory, err := privateBundleFile(store)
	if err != nil {
		return err
	}
	defer directory.Close()
	count := 0
	for {
		names, err := directory.Readdirnames(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, name := range names {
			count++
			if count > 100_000 {
				return errors.New("account import inventory exceeds inspection limit")
			}
			if !strings.HasPrefix(name, "import-") {
				continue
			}
			var receipt AccountImportReceipt
			if err := readPrivateJSON(store, name, 1<<20, &receipt); err != nil {
				return errors.New("account import inventory contains unreadable metadata")
			}
			if validateAccountImport(receipt) != nil || name != importReceiptName(receipt.TaskID) || receipt.NodeID != nodeID {
				return errors.New("account import inventory contains unverified identity")
			}
			if receipt.AccountID == accountID {
				if receipt.UserID != userID || receipt.State == "started" {
					return errors.New("account import requires recovery before takeover")
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
