package skillmanager

import (
	"errors"
	"os"
)

// AccountMigrationReceipt binds complete migration writer termination to the original copy intent.
type AccountMigrationReceipt struct {
	Version     int                `json:"version"`
	Copy        AccountCopyReceipt `json:"copy"`
	InputDigest string             `json:"input_digest"`
	State       string             `json:"state"`
}

func validateAccountMigration(record AccountMigrationReceipt) error {
	if record.Version != 1 && record.Version != 2 || record.Version == 2 && record.Copy.WriterVersion != 2 {
		return errors.New("unsupported account migration evidence version")
	}
	if record.Copy.State != "started" || !contentDigestPattern.MatchString(record.InputDigest) || validateAccountCopy(record.Copy) != nil {
		return errors.New("invalid account migration identity")
	}
	if record.State != "started" && record.State != "succeeded" && record.State != "failed" {
		return errors.New("invalid account migration outcome")
	}
	return nil
}
func accountMigrationName(task string) string { return "migration-" + accountCopyName(task) }

// ReadAccountMigration returns a private exact-input receipt without changing its original boot.
func ReadAccountMigration(root *os.Root, expected AccountMigrationReceipt) (AccountMigrationReceipt, error) {
	if err := validateAccountMigration(expected); err != nil {
		return AccountMigrationReceipt{}, err
	}
	saved, err := readAccountMigration(root, expected.Copy.TaskID)
	if err != nil {
		return saved, err
	}
	identity := saved
	identity.State = expected.State
	identity.Copy.BootID = expected.Copy.BootID
	// Preserve historical schema identity; the returned saved copy controls required phase proofs.
	identity.Version = expected.Version
	identity.Copy.WriterVersion = expected.Copy.WriterVersion
	if identity != expected {
		return saved, errors.New("account migration input differs from retained receipt")
	}
	return saved, nil
}
func readAccountMigration(root *os.Root, task string) (AccountMigrationReceipt, error) {
	private, err := privateBundleFile(root)
	if err != nil {
		return AccountMigrationReceipt{}, err
	}
	defer private.Close()
	var saved AccountMigrationReceipt
	if err := readPrivateJSON(root, accountMigrationName(task), 1<<20, &saved); err != nil {
		return saved, err
	}
	if err := validateAccountMigration(saved); err != nil {
		return saved, err
	}
	if saved.Copy.TaskID != task {
		return saved, errors.New("migration task differs")
	}
	return saved, nil
}

// BeginAccountMigration writes an immutable intent before any migration side effects.
func BeginAccountMigration(root *os.Root, record AccountMigrationReceipt) error {
	if err := validateAccountMigration(record); err != nil {
		return err
	}
	if record.State != "started" {
		return errors.New("migration must begin started")
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, accountMigrationName(record.Copy.TaskID), record, true)
}

// FinishAccountMigration records a terminal result only after all migration writers have exited.
func FinishAccountMigration(root *os.Root, expected AccountMigrationReceipt, state string) error {
	if err := requireNoMigrationRepair(root, expected.Copy.TaskID); err != nil {
		return err
	}
	if state != "succeeded" && state != "failed" {
		return errors.New("invalid terminal migration state")
	}
	saved, err := ReadAccountMigration(root, expected)
	if err != nil {
		return err
	}
	if saved.Copy.BootID != expected.Copy.BootID {
		return errors.New("migration belongs to another boot")
	}
	if err := requireMigrationWriterOutcomes(root, saved, state); err != nil {
		return err
	}
	if err := requireMigrationAttestation(root, saved, state); err != nil {
		return err
	}
	if saved.State == state {
		return nil
	}
	if saved.State != "started" {
		return errors.New("migration outcome is immutable")
	}
	copy, err := ReadAccountCopy(root, expected.Copy)
	if err != nil || copy.State != "copied" || copy.BootID != expected.Copy.BootID || copy.WriterVersion != saved.Copy.WriterVersion {
		return errors.New("cannot complete migration without its original copy")
	}
	saved.State = state
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, accountMigrationName(saved.Copy.TaskID), saved, false)
}

// CheckAccountMigrationWriter verifies original completion or independently completed source repair.
func CheckAccountMigrationWriter(root *os.Root, nodeID, userID, accountID, task string) error {
	if err := validateAccountIdentity(nodeID, userID, accountID); err != nil {
		return err
	}
	saved, err := readAccountMigration(root, task)
	if err != nil {
		return err
	}
	copy := saved.Copy
	if copy.NodeID != nodeID || copy.UserID != userID || copy.AccountID != accountID {
		return errors.New("migration writer evidence is incomplete or belongs to another account")
	}
	if saved.State == "started" {
		if _, err := ReadAccountMigrationRepairCompletion(root, copy.TaskID); err != nil {
			return errors.New("migration writer remains pending without independent repair completion")
		}
		return nil
	}
	if err := requireMigrationAttestation(root, saved, saved.State); err != nil {
		return err
	}
	if err := requireMigrationWriterOutcomes(root, saved, saved.State); err != nil {
		return err
	}
	completed, err := ReadAccountCopy(root, copy)
	if err != nil || completed.State != "copied" || completed.BootID != copy.BootID || completed.WriterVersion != copy.WriterVersion {
		return errors.New("migration copy evidence is incomplete")
	}
	return nil
}
