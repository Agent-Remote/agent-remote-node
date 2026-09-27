package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
)

// AccountMigrationRepairIntent binds independent repair to unchanged original evidence.
type AccountMigrationRepairIntent struct {
	Version              int                     `json:"version"`
	Migration            AccountMigrationReceipt `json:"migration"`
	OriginalTaskRecordID string                  `json:"original_task_record_id"`
	Source               string                  `json:"source_runtime_backend"`
	Target               string                  `json:"target_runtime_backend"`
	BaselineDigest       string                  `json:"baseline_digest"`
	EvidenceDigest       string                  `json:"evidence_digest"`
}

// AccountMigrationRepairAttempt pins one live recovery delivery without changing the intent.
type AccountMigrationRepairAttempt struct {
	Version        int    `json:"version"`
	OriginalTaskID string `json:"original_task_id"`
	RepairDigest   string `json:"repair_digest"`
	TaskID         string `json:"task_id"`
	TaskRecordID   string `json:"task_record_id"`
	LeaseAttempt   int64  `json:"lease_attempt"`
	BootID         string `json:"boot_id"`
}

// AccountMigrationRepairCompletion attests exact source restoration under one saved attempt.
type AccountMigrationRepairCompletion struct {
	Version int                           `json:"version"`
	Attempt AccountMigrationRepairAttempt `json:"attempt"`
}

func migrationRepairIntentName(task string) string {
	return "migration-repair-intent-" + accountCopyName(task)
}
func migrationRepairCompletionName(task string) string {
	return "migration-repair-complete-" + accountCopyName(task)
}
func migrationRepairAttemptName(attempt AccountMigrationRepairAttempt) string {
	identity := attempt.OriginalTaskID + "\x00" + attempt.TaskID + "\x00" + attempt.TaskRecordID + "\x00" + strconv.FormatInt(attempt.LeaseAttempt, 10) + "\x00" + attempt.BootID
	digest := sha256.Sum256([]byte(identity))
	return "migration-repair-attempt-" + hex.EncodeToString(digest[:]) + ".json"
}

func migrationRepairDigest(intent AccountMigrationRepairIntent) string {
	data, _ := json.Marshal(intent)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// PrepareAccountMigrationRepair reads the immutable input before the caller's repair preflight.
func PrepareAccountMigrationRepair(root *os.Root, original AccountMigrationReceipt, originalRecordID, source, target string) (AccountMigrationRepairIntent, error) {
	intent := AccountMigrationRepairIntent{Version: 1, Migration: original, OriginalTaskRecordID: originalRecordID, Source: source, Target: target}
	if validateAccountMigration(original) != nil || original.Version != 2 || original.State != "started" || validateAccountIdentity(originalRecordID) != nil ||
		source == target || (source != "native" && source != "docker_sandbox") || (target != "native" && target != "docker_sandbox") {
		return intent, errors.New("invalid original migration repair input")
	}
	baseline, err := ReadAccountMigrationBaseline(root, original)
	if err != nil {
		return intent, err
	}
	intent.BaselineDigest = migrationBaselineDigest(baseline)
	intent.EvidenceDigest, err = migrationRepairEvidenceDigest(root, intent)
	return intent, err
}

// ReadAccountMigrationRepairIntent requires unchanged original metadata and baseline.
func ReadAccountMigrationRepairIntent(root *os.Root, task string) (AccountMigrationRepairIntent, error) {
	var saved AccountMigrationRepairIntent
	if err := readPrivateJSON(root, migrationRepairIntentName(task), 1<<20, &saved); err != nil {
		return saved, err
	}
	if saved.Version != 1 || saved.Migration.Copy.TaskID != task {
		return saved, errors.New("invalid migration repair intent")
	}
	expected, err := PrepareAccountMigrationRepair(root, saved.Migration, saved.OriginalTaskRecordID, saved.Source, saved.Target)
	if err != nil || saved != expected {
		return saved, errors.New("migration repair evidence changed")
	}
	return saved, nil
}

// BeginAccountMigrationRepair fences the original before any independent permission write.
// The caller must already hold lifecycle exclusion and complete its live filesystem preflight.
func BeginAccountMigrationRepair(root *os.Root, intent AccountMigrationRepairIntent) error {
	expected, err := PrepareAccountMigrationRepair(root, intent.Migration, intent.OriginalTaskRecordID, intent.Source, intent.Target)
	if err != nil || expected != intent {
		return errors.New("migration repair intent no longer matches its original")
	}
	saved, readErr := ReadAccountMigrationRepairIntent(root, intent.Migration.Copy.TaskID)
	if readErr == nil {
		if saved != intent {
			return errors.New("migration repair intent is immutable")
		}
		return nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	return writeMigrationRepairRecord(root, migrationRepairIntentName(intent.Migration.Copy.TaskID), intent)
}

// NewAccountMigrationRepairAttempt binds a recovery task, lease attempt and boot to the intent.
func NewAccountMigrationRepairAttempt(intent AccountMigrationRepairIntent, task, record string, leaseAttempt int64, boot string) (AccountMigrationRepairAttempt, error) {
	attempt := AccountMigrationRepairAttempt{Version: 1, OriginalTaskID: intent.Migration.Copy.TaskID,
		RepairDigest: migrationRepairDigest(intent), TaskID: task, TaskRecordID: record, LeaseAttempt: leaseAttempt, BootID: boot}
	return attempt, validateMigrationRepairAttempt(intent, attempt)
}

func validateMigrationRepairAttempt(intent AccountMigrationRepairIntent, attempt AccountMigrationRepairAttempt) error {
	if intent.Version != 1 || validateAccountMigration(intent.Migration) != nil || intent.Migration.Version != 2 || intent.Migration.State != "started" ||
		validateAccountIdentity(intent.OriginalTaskRecordID) != nil || !contentDigestPattern.MatchString(intent.BaselineDigest) || !contentDigestPattern.MatchString(intent.EvidenceDigest) ||
		intent.Source == intent.Target || (intent.Source != "native" && intent.Source != "docker_sandbox") || (intent.Target != "native" && intent.Target != "docker_sandbox") {
		return errors.New("invalid migration repair intent identity")
	}

	prefix := "recover_tool_account_runtime:" + intent.Migration.Copy.AccountID + ":"
	key, ok := strings.CutPrefix(attempt.TaskID, prefix)
	if !ok || validateAccountIdentity(key, attempt.TaskRecordID, attempt.BootID) != nil || attempt.TaskRecordID == intent.OriginalTaskRecordID ||
		attempt.Version != 1 || attempt.LeaseAttempt <= 0 || attempt.OriginalTaskID != intent.Migration.Copy.TaskID || attempt.RepairDigest != migrationRepairDigest(intent) {
		return errors.New("invalid migration repair attempt")
	}
	return nil
}

// BeginAccountMigrationRepairAttempt durably records a fresh delivery before its synchronous writes.
func BeginAccountMigrationRepairAttempt(root *os.Root, intent AccountMigrationRepairIntent, attempt AccountMigrationRepairAttempt) error {
	saved, err := ReadAccountMigrationRepairIntent(root, intent.Migration.Copy.TaskID)
	if err != nil || saved != intent || validateMigrationRepairAttempt(intent, attempt) != nil {
		return errors.New("migration repair attempt lacks its original intent")
	}
	if _, err := root.Lstat(migrationRepairCompletionName(attempt.OriginalTaskID)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("completed migration repair cannot begin another writer")
	}
	if _, err := ReadAccountMigrationRepairAttempt(root, attempt); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeMigrationRepairRecord(root, migrationRepairAttemptName(attempt), attempt)
}

// ReadAccountMigrationRepairAttempt verifies the exact saved delivery and original intent.
func ReadAccountMigrationRepairAttempt(root *os.Root, expected AccountMigrationRepairAttempt) (AccountMigrationRepairAttempt, error) {
	var saved AccountMigrationRepairAttempt
	if err := readPrivateJSON(root, migrationRepairAttemptName(expected), 1<<20, &saved); err != nil {
		return saved, err
	}
	intent, err := ReadAccountMigrationRepairIntent(root, expected.OriginalTaskID)
	if err != nil || saved != expected || validateMigrationRepairAttempt(intent, saved) != nil {
		return saved, errors.New("migration repair attempt changed")
	}
	return saved, nil
}

// FinishAccountMigrationRepair publishes separate immutable restoration evidence.
// Callers must first prove stable boot, quiescence, exact source/parent permissions,
// complete historical content and durability under the current live authorization.
func FinishAccountMigrationRepair(root *os.Root, attempt AccountMigrationRepairAttempt) error {
	if _, err := ReadAccountMigrationRepairAttempt(root, attempt); err != nil {
		return err
	}
	expected := AccountMigrationRepairCompletion{Version: 1, Attempt: attempt}
	saved, err := ReadAccountMigrationRepairCompletion(root, attempt.OriginalTaskID)
	if err == nil {
		if saved != expected {
			return errors.New("migration repair completion is immutable")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeMigrationRepairRecord(root, migrationRepairCompletionName(attempt.OriginalTaskID), expected)
}

// ReadAccountMigrationRepairCompletion verifies its saved attempt without settling old receipts.
func ReadAccountMigrationRepairCompletion(root *os.Root, task string) (AccountMigrationRepairCompletion, error) {
	var saved AccountMigrationRepairCompletion
	if err := readPrivateJSON(root, migrationRepairCompletionName(task), 1<<20, &saved); err != nil {
		return saved, err
	}
	if saved.Version != 1 || saved.Attempt.OriginalTaskID != task {
		return saved, errors.New("invalid migration repair completion")
	}
	if _, err := ReadAccountMigrationRepairAttempt(root, saved.Attempt); err != nil {
		return saved, err
	}
	return saved, nil
}

func writeMigrationRepairRecord(root *os.Root, name string, record any) error {
	private, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer private.Close()
	return writePrivateJSON(root, private, name, record, true)
}

func requireNoMigrationRepair(root *os.Root, task string) error {
	if _, err := root.Lstat(migrationRepairIntentName(task)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("original migration is fenced by independent repair")
	}
	return nil
}
