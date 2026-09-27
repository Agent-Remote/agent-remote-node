package skillmanager

import (
	"errors"
	"os"
)

type migrationAttestation struct {
	Version        int                     `json:"version"`
	Migration      AccountMigrationReceipt `json:"migration"`
	BaselineDigest string                  `json:"baseline_digest"`
	Outcome        string                  `json:"outcome"`
}

func migrationAttestationName(task string) string {
	return "migration-attestation-" + accountCopyName(task)
}

func expectedMigrationAttestation(root *os.Root, original AccountMigrationReceipt, outcome string) (migrationAttestation, error) {
	var record migrationAttestation
	if outcome != "succeeded" && outcome != "failed" {
		return record, errors.New("invalid attested migration outcome")
	}
	baseline, err := ReadAccountMigrationBaseline(root, original)
	if err != nil {
		return record, err
	}
	return migrationAttestation{Version: 1, Migration: baseline.Migration, BaselineDigest: migrationBaselineDigest(baseline), Outcome: outcome}, nil
}

// AttestAccountMigration records the Helper's repeated, synchronized baseline verification.
// Callers own writer exclusion and must verify exact source permissions before attesting failure.
func AttestAccountMigration(root *os.Root, original AccountMigrationReceipt, outcome string) error {
	if err := requireNoMigrationRepair(root, original.Copy.TaskID); err != nil {
		return err
	}
	expected, err := expectedMigrationAttestation(root, original, outcome)
	if err != nil {
		return err
	}
	saved, err := readAccountMigration(root, original.Copy.TaskID)
	if err != nil || saved != expected.Migration || requireMigrationWriterOutcomes(root, saved, outcome) != nil {
		return errors.New("migration cannot attest without original completed writers")
	}
	copy, err := ReadAccountCopy(root, saved.Copy)
	if err != nil || copy.State != "copied" || copy.BootID != saved.Copy.BootID || copy.WriterVersion != saved.Copy.WriterVersion {
		return errors.New("migration attestation lacks original completed copy")
	}
	var existing migrationAttestation
	err = readPrivateJSON(root, migrationAttestationName(original.Copy.TaskID), 1<<20, &existing)
	if err == nil {
		if existing != expected {
			return errors.New("migration attestation is immutable")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, migrationAttestationName(original.Copy.TaskID), expected, true)
}

func requireMigrationAttestation(root *os.Root, original AccountMigrationReceipt, outcome string) error {
	if original.Version == 1 {
		return nil
	}
	expected, err := expectedMigrationAttestation(root, original, outcome)
	if err != nil {
		return err
	}
	var saved migrationAttestation
	if err := readPrivateJSON(root, migrationAttestationName(original.Copy.TaskID), 1<<20, &saved); err != nil {
		return err
	}
	if saved != expected {
		return errors.New("migration attestation differs from original baseline")
	}
	return nil
}
