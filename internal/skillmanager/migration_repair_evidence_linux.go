package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

type migrationRepairEvidence struct {
	Migration   AccountMigrationReceipt   `json:"migration"`
	Copy        AccountCopyReceipt        `json:"copy"`
	Target      *MigrationOwnershipIntent `json:"target"`
	Rollback    *MigrationOwnershipIntent `json:"rollback"`
	Writers     [7]MigrationWriterReceipt `json:"writers"`
	Attestation *migrationAttestation     `json:"attestation"`
}

func migrationRepairEvidenceDigest(root *os.Root, intent AccountMigrationRepairIntent) (string, error) {
	original := intent.Migration
	saved, err := readAccountMigration(root, original.Copy.TaskID)
	if err != nil || saved != original {
		return "", errors.New("original migration changed before repair")
	}
	copy, err := ReadAccountCopy(root, original.Copy)
	if err != nil || copy.State != "copied" || copy.WriterVersion != 2 || copy.BootID != original.Copy.BootID {
		return "", errors.New("migration repair requires the original completed backup")
	}
	evidence := migrationRepairEvidence{Migration: saved, Copy: copy}
	for _, phase := range []string{"target", "rollback"} {
		ownership, err := ReadMigrationOwnership(root, original.Copy, phase)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || phase == "target" && ownership.Backend != intent.Target || phase == "rollback" && ownership.Backend != intent.Source {
			return "", errors.New("migration repair ownership direction differs")
		}
		if phase == "target" {
			evidence.Target = &ownership
		} else {
			evidence.Rollback = &ownership
		}
	}
	if evidence.Rollback != nil && evidence.Target == nil {
		return "", errors.New("migration repair contains orphaned rollback")
	}
	for index, phase := range []string{"copy", "target-0", "target-1", "target-2", "rollback-0", "rollback-1", "rollback-2"} {
		writer, err := ReadMigrationPhase(root, original.Copy, phase)
		if errors.Is(err, os.ErrNotExist) && index != 0 {
			continue
		}
		if err != nil || index == 0 && writer.State != "succeeded" {
			return "", errors.New("migration repair writer evidence is invalid")
		}
		evidence.Writers[index] = writer
	}
	var attestation migrationAttestation
	err = readPrivateJSON(root, migrationAttestationName(original.Copy.TaskID), 1<<20, &attestation)
	if err == nil {
		if requireMigrationAttestation(root, original, attestation.Outcome) != nil || requireMigrationWriterOutcomes(root, original, attestation.Outcome) != nil {
			return "", errors.New("original migration attestation is invalid")
		}
		evidence.Attestation = &attestation
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func readMigrationRepairEvidenceOwner(root *os.Root, name string) (AccountMigrationReceipt, error) {
	var original AccountMigrationReceipt
	var task string
	switch {
	case strings.HasPrefix(name, "migration-repair-intent-"):
		var intent AccountMigrationRepairIntent
		if err := readPrivateJSON(root, name, 1<<20, &intent); err != nil {
			return original, err
		}
		task = intent.Migration.Copy.TaskID
		if name != migrationRepairIntentName(task) {
			return original, errors.New("migration repair intent filename differs")
		}
	case strings.HasPrefix(name, "migration-repair-attempt-"):
		var attempt AccountMigrationRepairAttempt
		if err := readPrivateJSON(root, name, 1<<20, &attempt); err != nil {
			return original, err
		}
		task = attempt.OriginalTaskID
		if name != migrationRepairAttemptName(attempt) {
			return original, errors.New("migration repair attempt filename differs")
		}
		if _, err := ReadAccountMigrationRepairAttempt(root, attempt); err != nil {
			return original, err
		}
	case strings.HasPrefix(name, "migration-repair-complete-"):
		var completion AccountMigrationRepairCompletion
		if err := readPrivateJSON(root, name, 1<<20, &completion); err != nil {
			return original, err
		}
		task = completion.Attempt.OriginalTaskID
		if name != migrationRepairCompletionName(task) {
			return original, errors.New("migration repair completion filename differs")
		}
		if _, err := ReadAccountMigrationRepairCompletion(root, task); err != nil {
			return original, err
		}
	default:
		return original, errors.New("unknown migration repair evidence")
	}
	intent, err := ReadAccountMigrationRepairIntent(root, task)
	return intent.Migration, err
}
