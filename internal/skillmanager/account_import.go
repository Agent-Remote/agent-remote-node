package skillmanager

import (
	"errors"
	"regexp"
)

var accountIdentityPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// AccountFence closes legacy skill imports and runtime writers for one helper-owned account identity.
type AccountFence struct {
	Version        int    `json:"version"`
	NodeID         string `json:"node_id"`
	UserID         string `json:"user_id"`
	AccountID      string `json:"account_id"`
	DirectoryEpoch int64  `json:"directory_epoch"`
}

// AccountImportReceipt records completion without retaining configuration bytes or file contents.
type AccountImportReceipt struct {
	Version     int    `json:"version"`
	TaskID      string `json:"task_id"`
	NodeID      string `json:"node_id"`
	UserID      string `json:"user_id"`
	AccountID   string `json:"account_id"`
	InputDigest string `json:"input_digest"`
	State       string `json:"state"`
}

func validateAccountFence(record AccountFence) error {
	if record.Version != 1 || record.DirectoryEpoch <= 0 {
		return errors.New("invalid account skill fence version or epoch")
	}
	return validateAccountIdentity(record.NodeID, record.UserID, record.AccountID)
}

func validateAccountIdentity(values ...string) error {
	for _, value := range values {
		if !accountIdentityPattern.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
			return errors.New("invalid account skill identity")
		}
	}
	return nil
}
