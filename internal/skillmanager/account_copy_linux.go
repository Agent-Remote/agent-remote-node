package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// AccountCopyReceipt binds one immutable backend-migration backup and its proven copy exit.
// It does not prove completion of later ownership changes or permit account takeover.
type AccountCopyReceipt struct {
	Version       int    `json:"version"`
	WriterVersion int    `json:"writer_version,omitempty"`
	NodeID        string `json:"node_id"`
	UserID        string `json:"user_id"`
	AccountID     string `json:"account_id"`
	TaskID        string `json:"task_id"`
	InputDigest   string `json:"input_digest"`
	BootID        string `json:"boot_id"`
	Unit          string `json:"unit"`
	State         string `json:"state"`
}

// AccountCopyUnit derives the only permitted copy service name from the exact logical task.
func AccountCopyUnit(taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return "agent-remote-copy-" + hex.EncodeToString(digest[:16]) + ".service"
}

func validateAccountCopy(record AccountCopyReceipt) error {
	if err := validateAccountIdentity(record.NodeID, record.UserID, record.AccountID, record.BootID); err != nil {
		return err
	}
	prefix := "migrate_tool_account_runtime:" + record.AccountID + ":"
	suffix := strings.TrimPrefix(record.TaskID, prefix)
	if (record.WriterVersion < 0 || record.WriterVersion > 2) || record.Version != 1 || !strings.HasPrefix(record.TaskID, prefix) || validateAccountIdentity(suffix) != nil || !contentDigestPattern.MatchString(record.InputDigest) || record.Unit != AccountCopyUnit(record.TaskID) {
		return errors.New("invalid account copy identity")
	}
	if record.State != "started" && record.State != "copied" && record.State != "failed" {
		return errors.New("invalid account copy state")
	}
	return nil
}
func accountCopyName(taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return "account-copy-" + hex.EncodeToString(digest[:]) + ".json"
}

// ReadAccountCopy verifies the exact immutable input; a prior boot is retained as evidence, not rewritten.
func ReadAccountCopy(root *os.Root, expected AccountCopyReceipt) (AccountCopyReceipt, error) {
	if err := validateAccountCopy(expected); err != nil {
		return AccountCopyReceipt{}, err
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return AccountCopyReceipt{}, err
	}
	defer private.Close()
	var saved AccountCopyReceipt
	if err := readPrivateJSON(root, accountCopyName(expected.TaskID), 1<<20, &saved); err != nil {
		return saved, err
	}
	if err := validateAccountCopy(saved); err != nil {
		return saved, err
	}
	identity := saved
	identity.State = expected.State
	identity.BootID = expected.BootID
	// The persisted version selects proof requirements; callers cannot upgrade old evidence.
	identity.WriterVersion = expected.WriterVersion
	if identity != expected {
		return saved, errors.New("account copy input differs from retained receipt")
	}
	return saved, nil
}

// BeginAccountCopy persists intent before any copy process can be launched; callers serialize mutations.
func BeginAccountCopy(root *os.Root, record AccountCopyReceipt) error {
	if err := requireNoMigrationRepair(root, record.TaskID); err != nil {
		return err
	}
	if err := validateAccountCopy(record); err != nil {
		return err
	}
	if record.State != "started" {
		return errors.New("account copy must begin in started state")
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, accountCopyName(record.TaskID), record, true)
}

// FinishAccountCopy records copy-only completion after the caller proves the entire service cgroup empty.
func FinishAccountCopy(root *os.Root, expected AccountCopyReceipt, state string) error {
	if err := requireNoMigrationRepair(root, expected.TaskID); err != nil {
		return err
	}
	if state != "copied" && state != "failed" {
		return errors.New("invalid terminal copy state")
	}
	saved, err := ReadAccountCopy(root, expected)
	if err != nil {
		return err
	}
	if saved.BootID != expected.BootID {
		return errors.New("cannot finish account copy from another boot")
	}
	outcome := "succeeded"
	if state == "failed" {
		outcome = "failed"
	}
	if err := requireCopyWriterOutcome(root, saved, outcome); err != nil {
		return err
	}
	if saved.State == state {
		return nil
	}
	if saved.State != "started" {
		return errors.New("account copy receipt is already terminal")
	}
	saved.State = state
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, accountCopyName(saved.TaskID), saved, false)
}
