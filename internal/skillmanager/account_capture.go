package skillmanager

import "errors"

// AccountTakeoverBinding fixes the exact Server reservation independently of runtime directories.
type AccountTakeoverBinding struct {
	Version         int    `json:"version"`
	NodeID          string `json:"node_id"`
	UserID          string `json:"user_id"`
	AccountID       string `json:"account_id"`
	TakeoverID      string `json:"takeover_id"`
	TaskID          string `json:"task_id"`
	RuntimeBackend  string `json:"runtime_backend"`
	DirectoryEpoch  int64  `json:"directory_epoch"`
	InventoryDigest string `json:"inventory_digest"`
}

// AccountCapture records the original, immutable Helper capture without exposing a host path.
type AccountCapture struct {
	Version         int                    `json:"version"`
	Binding         AccountTakeoverBinding `json:"binding"`
	HelperReceiptID string                 `json:"helper_receipt_id"`
	TreeDigest      string                 `json:"tree_digest"`
	SourceExists    bool                   `json:"source_exists"`
}

// Validate checks the retained capture identity without opening its source or private bundle.
func (c AccountCapture) Validate() error {
	if err := c.Binding.Validate(); err != nil {
		return err
	}
	if err := validateAccountIdentity(c.HelperReceiptID); err != nil {
		return err
	}
	if c.Version != 1 || !contentDigestPattern.MatchString(c.TreeDigest) {
		return errors.New("invalid account capture identity")
	}
	return nil
}

// Validate checks the capture reservation before filesystem access.
func (b AccountTakeoverBinding) Validate() error {
	if err := validateAccountIdentity(b.NodeID, b.UserID, b.AccountID, b.TakeoverID, b.TaskID); err != nil {
		return err
	}
	if b.Version != 1 || b.DirectoryEpoch <= 0 || !contentDigestPattern.MatchString(b.InventoryDigest) ||
		(b.RuntimeBackend != "native" && b.RuntimeBackend != "docker_sandbox") {
		return errors.New("invalid account takeover binding")
	}
	return nil
}
