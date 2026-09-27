package skillmanager

import (
	"errors"
	"io"
	"os"
	"strings"
)

// CheckAccountCopyHistory requires complete writer evidence for every local migration and copy record.
func CheckAccountCopyHistory(store *os.Root, nodeID, userID, accountID string) error {
	if _, err := ReadAccountFence(store, nodeID, userID, accountID); err != nil {
		return err
	}
	return CheckAccountMigrationHistory(store, nodeID, userID, accountID)
}

// CheckAccountMigrationHistory rejects unresolved local writers before a new migration can start.
func CheckAccountMigrationHistory(store *os.Root, nodeID, userID, accountID string) error {
	if err := validateAccountIdentity(nodeID, userID, accountID); err != nil {
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
				return errors.New("account copy inventory exceeds inspection limit")
			}
			if strings.HasPrefix(name, "migration-repair-") {
				original, err := readMigrationRepairEvidenceOwner(store, name)
				if err != nil || original.Copy.NodeID != nodeID {
					return errors.New("local migration repair evidence is invalid")
				}
				if original.Copy.AccountID == accountID {
					if err := CheckAccountMigrationWriter(store, nodeID, userID, accountID, original.Copy.TaskID); err != nil {
						return err
					}
				}
				continue
			}
			if strings.HasPrefix(name, "migration-baseline-") || strings.HasPrefix(name, "migration-attestation-") {
				original, err := readMigrationEvidenceOwner(store, name)
				if err != nil || original.Copy.NodeID != nodeID {
					return errors.New("local migration baseline or attestation is invalid")
				}
				if original.Copy.AccountID == accountID {
					if err := CheckAccountMigrationWriter(store, nodeID, userID, accountID, original.Copy.TaskID); err != nil {
						return err
					}
				}
				continue
			}
			if strings.HasPrefix(name, "migration-ownership-") {
				ownership, err := readMigrationOwnership(store, name)
				if err != nil || ownership.Copy.NodeID != nodeID {
					return errors.New("local ownership intent is invalid")
				}
				if ownership.Copy.AccountID == accountID {
					if err := CheckAccountMigrationWriter(store, nodeID, userID, accountID, ownership.Copy.TaskID); err != nil {
						return err
					}
				}
				continue
			}
			if strings.HasPrefix(name, "migration-writer-") {
				writer, err := readMigrationWriter(store, name)
				if err != nil || writer.Identity.Copy.NodeID != nodeID {
					return errors.New("local writer evidence is invalid")
				}
				if writer.Identity.Copy.AccountID == accountID {
					if err := CheckAccountMigrationWriter(store, nodeID, userID, accountID, writer.Identity.Copy.TaskID); err != nil {
						return err
					}
				}
				continue
			}
			if strings.HasPrefix(name, "migration-account-copy-") {
				var migration AccountMigrationReceipt
				if readPrivateJSON(store, name, 1<<20, &migration) != nil || validateAccountMigration(migration) != nil || name != accountMigrationName(migration.Copy.TaskID) || migration.Copy.NodeID != nodeID {
					return errors.New("local migration evidence is invalid")
				}
				if migration.Copy.AccountID == accountID {
					if err := CheckAccountMigrationWriter(store, nodeID, userID, accountID, migration.Copy.TaskID); err != nil {
						return err
					}
				}
				continue
			}
			if !strings.HasPrefix(name, "account-copy-") {
				continue
			}
			var record AccountCopyReceipt
			if readPrivateJSON(store, name, 1<<20, &record) != nil || validateAccountCopy(record) != nil || name != accountCopyName(record.TaskID) || record.NodeID != nodeID {
				return errors.New("account copy inventory contains unverified metadata")
			}
			if record.AccountID == accountID {
				if err := CheckAccountMigrationWriter(store, nodeID, userID, accountID, record.TaskID); err != nil {
					return err
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
