package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

// MigrationOwnershipIntent precedes every direct ownership write, including rollback before its ACLs.
type MigrationOwnershipIntent struct {
	Version int                `json:"version"`
	Copy    AccountCopyReceipt `json:"copy"`
	Phase   string             `json:"phase"`
	Backend string             `json:"backend"`
}

func migrationOwnershipName(task, phase string) string {
	digest := sha256.Sum256([]byte(task + "\x00" + phase))
	return "migration-ownership-" + hex.EncodeToString(digest[:]) + ".json"
}

func validateMigrationOwnership(intent MigrationOwnershipIntent) error {
	if intent.Version != 1 || validateAccountCopy(intent.Copy) != nil || intent.Copy.State != "started" || intent.Copy.WriterVersion != 2 ||
		intent.Phase != "target" && intent.Phase != "rollback" || intent.Backend != "native" && intent.Backend != "docker_sandbox" {
		return errors.New("invalid migration ownership intent")
	}
	return nil
}

func readMigrationOwnership(root *os.Root, name string) (MigrationOwnershipIntent, error) {
	var intent MigrationOwnershipIntent
	if err := readPrivateJSON(root, name, 1<<20, &intent); err != nil {
		return intent, err
	}
	if err := validateMigrationOwnership(intent); err != nil {
		return intent, err
	}
	if name != migrationOwnershipName(intent.Copy.TaskID, intent.Phase) {
		return intent, errors.New("ownership intent filename differs")
	}
	copy, err := ReadAccountCopy(root, intent.Copy)
	if err != nil || copy.WriterVersion != 2 || copy.BootID != intent.Copy.BootID || copy.State != "copied" {
		return intent, errors.New("ownership lacks original completed copy")
	}
	migration, err := readAccountMigration(root, intent.Copy.TaskID)
	if err != nil || migration.Copy != intent.Copy {
		return intent, errors.New("ownership lacks original migration")
	}
	return intent, nil
}

// ReadMigrationOwnership returns the original ownership direction without upgrading old evidence.
func ReadMigrationOwnership(root *os.Root, copy AccountCopyReceipt, phase string) (MigrationOwnershipIntent, error) {
	if phase != "target" && phase != "rollback" {
		return MigrationOwnershipIntent{}, errors.New("invalid ownership phase")
	}
	intent, err := readMigrationOwnership(root, migrationOwnershipName(copy.TaskID, phase))
	if err == nil && intent.Copy != copy {
		err = errors.New("ownership intent belongs to another copy")
	}
	return intent, err
}

// BeginMigrationOwnership durably fixes the phase and backend before identity lookup or any Lchown.
func BeginMigrationOwnership(root *os.Root, intent MigrationOwnershipIntent) error {
	if err := requireNoMigrationRepair(root, intent.Copy.TaskID); err != nil {
		return err
	}
	if err := validateMigrationOwnership(intent); err != nil {
		return err
	}
	copy, err := ReadAccountCopy(root, intent.Copy)
	if err != nil || copy.State != "copied" || copy.WriterVersion != 2 || copy.BootID != intent.Copy.BootID {
		return errors.New("original copy has not completed")
	}
	migration, err := readAccountMigration(root, intent.Copy.TaskID)
	if err != nil || migration.Copy != intent.Copy || migration.State != "started" {
		return errors.New("original migration is not pending")
	}
	if migration.Version == 2 {
		if _, err := ReadAccountMigrationBaseline(root, migration); err != nil {
			return err
		}
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, migrationOwnershipName(intent.Copy.TaskID, intent.Phase), intent, true)
}

func requireMigrationOwnershipOutcomes(root *os.Root, migration AccountMigrationReceipt, outcome string) error {
	if migration.Copy.WriterVersion != 2 {
		return nil
	}
	target, err := ReadMigrationOwnership(root, migration.Copy, "target")
	if err != nil {
		return errors.New("target ownership intent is unavailable")
	}
	rollback, err := ReadMigrationOwnership(root, migration.Copy, "rollback")
	if outcome == "succeeded" && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if outcome != "failed" || err != nil || rollback.Backend == target.Backend {
		return errors.New("migration ownership direction is incomplete or inconsistent")
	}
	return nil
}
