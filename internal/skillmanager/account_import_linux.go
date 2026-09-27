package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// ReadAccountFence returns a verified immutable fence, or os.ErrNotExist for an unfenced account.
func ReadAccountFence(root *os.Root, nodeID, userID, accountID string) (AccountFence, error) {
	if err := validateAccountIdentity(nodeID, userID, accountID); err != nil {
		return AccountFence{}, err
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return AccountFence{}, err
	}
	defer private.Close()
	var record AccountFence
	if err := readPrivateJSON(root, "account-"+accountID+".json", 1<<20, &record); err != nil {
		return AccountFence{}, err
	}
	if err := validateAccountFence(record); err != nil {
		return AccountFence{}, err
	}
	if record.NodeID != nodeID || record.UserID != userID || record.AccountID != accountID {
		return AccountFence{}, errors.New("account skill fence identity mismatch")
	}
	return record, nil
}

// CloseAccountImports publishes a permanent fence; later grants cannot reopen or replace it.
func CloseAccountImports(root *os.Root, record AccountFence) (AccountFence, error) {
	if err := validateAccountFence(record); err != nil {
		return AccountFence{}, err
	}
	current, err := ReadAccountFence(root, record.NodeID, record.UserID, record.AccountID)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return AccountFence{}, err
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return AccountFence{}, err
	}
	defer private.Close()
	if err := writePrivateJSON(root, private, "account-"+record.AccountID+".json", record, true); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ReadAccountFence(root, record.NodeID, record.UserID, record.AccountID)
		}
		return AccountFence{}, err
	}
	return record, nil
}

// ReadAccountImport checks the exact task and immutable input before returning a saved outcome.
func ReadAccountImport(root *os.Root, expected AccountImportReceipt) (AccountImportReceipt, error) {
	if err := validateAccountImport(expected); err != nil {
		return AccountImportReceipt{}, err
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return AccountImportReceipt{}, err
	}
	defer private.Close()
	var record AccountImportReceipt
	if err := readPrivateJSON(root, importReceiptName(expected.TaskID), 1<<20, &record); err != nil {
		return AccountImportReceipt{}, err
	}
	if err := validateAccountImport(record); err != nil {
		return AccountImportReceipt{}, err
	}
	identity := record
	identity.State = expected.State
	if identity != expected {
		return AccountImportReceipt{}, errors.New("config import task input differs from its retained receipt")
	}
	return record, nil
}

// BeginAccountImport durably records the write intent before any account file can change.
func BeginAccountImport(root *os.Root, record AccountImportReceipt) error {
	if err := validateAccountImport(record); err != nil {
		return err
	}
	if record.State != "started" {
		return errors.New("config import must begin in started state")
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, importReceiptName(record.TaskID), record, true)
}

// FinishAccountImport advances only the matching started receipt under helper serialization.
func FinishAccountImport(root *os.Root, expected AccountImportReceipt, state string) error {
	if state != "succeeded" && state != "failed" {
		return errors.New("invalid config import terminal state")
	}
	current, err := ReadAccountImport(root, expected)
	if err != nil {
		return err
	}
	if current.State == state {
		return nil
	}
	if current.State != "started" {
		return errors.New("config import receipt is already terminal")
	}
	current.State = state
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, importReceiptName(current.TaskID), current, false)
}

func validateAccountImport(record AccountImportReceipt) error {
	if err := validateAccountIdentity(record.NodeID, record.UserID, record.AccountID); err != nil {
		return err
	}
	prefix := "import_tool_account_config:" + record.AccountID + ":"
	if record.Version != 1 || !strings.HasPrefix(record.TaskID, prefix) || !accountIdentityPattern.MatchString(strings.TrimPrefix(record.TaskID, prefix)) ||
		!contentDigestPattern.MatchString(record.InputDigest) || (record.State != "started" && record.State != "succeeded" && record.State != "failed") {
		return errors.New("invalid config import receipt")
	}
	return nil
}

func importReceiptName(taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return "import-" + hex.EncodeToString(digest[:]) + ".json"
}
