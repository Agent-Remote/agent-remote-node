package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
)

// MigrationWriterIdentity fixes one copy or ACL command to the original account and boot.
type MigrationWriterIdentity struct {
	Copy          AccountCopyReceipt `json:"copy"`
	Phase         string             `json:"phase"`
	CommandDigest string             `json:"command_digest"`
}

// MigrationWriterReceipt retains one privileged launch and its immutable observed invocation.
type MigrationWriterReceipt struct {
	Version      int                     `json:"version"`
	Identity     MigrationWriterIdentity `json:"identity"`
	LaunchID     string                  `json:"launch_id"`
	State        string                  `json:"state"`
	InvocationID string                  `json:"invocation_id"`
}

// MigrationWriterUnit derives the only service name allowed for a validated phase.
func MigrationWriterUnit(identity MigrationWriterIdentity) (string, error) {
	if !contentDigestPattern.MatchString(identity.CommandDigest) {
		return "", errors.New("invalid migration writer command identity")
	}
	return migrationWriterUnit(identity.Copy, identity.Phase)
}

func migrationWriterUnit(copy AccountCopyReceipt, phaseName string) (string, error) {
	if validateAccountCopy(copy) != nil || copy.State != "started" || (copy.WriterVersion != 1 && copy.WriterVersion != 2) {
		return "", errors.New("invalid migration writer identity")
	}
	if phaseName == "copy" {
		return copy.Unit, nil
	}
	for _, phase := range []string{"target", "rollback"} {
		for index := 0; index < 3; index++ {
			if phaseName == phase+"-"+strconv.Itoa(index) {
				digest := sha256.Sum256([]byte(copy.TaskID + ":" + phase + ":" + strconv.Itoa(index)))
				return "agent-remote-own-" + hex.EncodeToString(digest[:16]) + ".service", nil
			}
		}
	}
	return "", errors.New("invalid migration writer phase")
}

func validateMigrationWriter(record MigrationWriterReceipt) error {
	if _, err := MigrationWriterUnit(record.Identity); err != nil {
		return err
	}
	if record.Version != 1 || !validSkillUUID(record.LaunchID) {
		return errors.New("invalid migration launch authority")
	}
	if record.State == "starting" && record.InvocationID == "" {
		return nil
	}
	if (record.State == "observed" || record.State == "succeeded" || record.State == "failed") && invocationPattern.MatchString(record.InvocationID) && record.InvocationID != "00000000000000000000000000000000" {
		return nil
	}
	return errors.New("invalid migration writer state")
}

func migrationWriterName(task, phase string) string {
	digest := sha256.Sum256([]byte(task + "\x00" + phase))
	return "migration-writer-" + hex.EncodeToString(digest[:]) + ".json"
}

func readMigrationWriter(root *os.Root, name string) (MigrationWriterReceipt, error) {
	var saved MigrationWriterReceipt
	if err := readPrivateJSON(root, name, 1<<20, &saved); err != nil {
		return saved, err
	}
	if err := validateMigrationWriter(saved); err != nil {
		return saved, err
	}
	if name != migrationWriterName(saved.Identity.Copy.TaskID, saved.Identity.Phase) {
		return saved, errors.New("migration writer filename differs")
	}
	copy, err := ReadAccountCopy(root, saved.Identity.Copy)
	if err != nil || copy.BootID != saved.Identity.Copy.BootID || copy.WriterVersion != saved.Identity.Copy.WriterVersion {
		return saved, errors.New("original migration copy authority is unavailable")
	}
	if err := requireOwnershipForWriter(root, saved.Identity); err != nil {
		return saved, err
	}
	return saved, nil
}

// ReadMigrationWriter reads exact original authority without adopting another boot or command.
func ReadMigrationWriter(root *os.Root, identity MigrationWriterIdentity) (MigrationWriterReceipt, error) {
	if _, err := MigrationWriterUnit(identity); err != nil {
		return MigrationWriterReceipt{}, err
	}
	saved, err := readMigrationWriter(root, migrationWriterName(identity.Copy.TaskID, identity.Phase))
	if err == nil && saved.Identity != identity {
		err = errors.New("migration writer input differs")
	}
	return saved, err
}

// BeginMigrationWriter publishes launch authority before starting its privileged service.
func BeginMigrationWriter(root *os.Root, identity MigrationWriterIdentity) (MigrationWriterReceipt, error) {
	if err := requireNoMigrationRepair(root, identity.Copy.TaskID); err != nil {
		return MigrationWriterReceipt{}, err
	}
	record := MigrationWriterReceipt{Version: 1, Identity: identity, LaunchID: newCaptureID(), State: "starting"}
	if err := validateMigrationWriter(record); err != nil {
		return record, err
	}
	copy, err := ReadAccountCopy(root, identity.Copy)
	if err != nil || copy.BootID != identity.Copy.BootID || copy.WriterVersion != identity.Copy.WriterVersion {
		return record, errors.New("missing original copy authority")
	}
	if err := requireOwnershipForWriter(root, identity); err != nil {
		return record, err
	}
	if identity.Phase == "copy" && copy.State != "started" || identity.Phase != "copy" && copy.State != "copied" {
		return record, errors.New("migration phase cannot start before its original copy state")
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return record, err
	}
	defer private.Close()
	return record, writePrivateJSON(root, private, migrationWriterName(identity.Copy.TaskID, identity.Phase), record, true)
}

// ObserveMigrationWriter pins the first verified invocation; no observation can replace it.
func ObserveMigrationWriter(root *os.Root, expected MigrationWriterReceipt, invocation string) (MigrationWriterReceipt, error) {
	if err := requireNoMigrationRepair(root, expected.Identity.Copy.TaskID); err != nil {
		return MigrationWriterReceipt{}, err
	}
	if err := validateMigrationWriter(expected); err != nil {
		return MigrationWriterReceipt{}, err
	}
	saved, err := ReadMigrationWriter(root, expected.Identity)
	if err != nil {
		return saved, err
	}
	if saved.LaunchID != expected.LaunchID {
		return saved, errors.New("migration launch differs")
	}
	if saved.State != "starting" {
		if saved.InvocationID != invocation {
			return saved, errors.New("migration invocation differs")
		}
		return saved, nil
	}
	if expected != saved {
		return saved, errors.New("stale migration observation")
	}
	saved.State, saved.InvocationID = "observed", invocation
	return saveMigrationWriter(root, saved)
}

// FinishMigrationWriter records verified whole-cgroup termination for the observed invocation.
func FinishMigrationWriter(root *os.Root, expected MigrationWriterReceipt, outcome string) (MigrationWriterReceipt, error) {
	if err := requireNoMigrationRepair(root, expected.Identity.Copy.TaskID); err != nil {
		return MigrationWriterReceipt{}, err
	}
	if err := validateMigrationWriter(expected); err != nil {
		return MigrationWriterReceipt{}, err
	}
	if outcome != "succeeded" && outcome != "failed" {
		return MigrationWriterReceipt{}, errors.New("invalid migration writer outcome")
	}
	saved, err := ReadMigrationWriter(root, expected.Identity)
	if err != nil {
		return saved, err
	}
	if saved.LaunchID != expected.LaunchID || saved.InvocationID != expected.InvocationID || saved.InvocationID == "" {
		return saved, errors.New("migration writer completion authority differs")
	}
	if saved.State == outcome {
		return saved, nil
	}
	if saved.State != "observed" || expected.State != "observed" {
		return saved, errors.New("migration writer outcome is immutable")
	}
	saved.State = outcome
	return saveMigrationWriter(root, saved)
}

func saveMigrationWriter(root *os.Root, record MigrationWriterReceipt) (MigrationWriterReceipt, error) {
	if err := validateMigrationWriter(record); err != nil {
		return record, err
	}
	private, err := privateBundleFile(root)
	if err != nil {
		return record, err
	}
	defer private.Close()
	return record, writePrivateJSON(root, private, migrationWriterName(record.Identity.Copy.TaskID, record.Identity.Phase), record, false)
}

// ReadMigrationPhase discovers one original phase without accepting a caller-selected command digest.
func ReadMigrationPhase(root *os.Root, copy AccountCopyReceipt, phase string) (MigrationWriterReceipt, error) {
	if _, err := migrationWriterUnit(copy, phase); err != nil {
		return MigrationWriterReceipt{}, err
	}
	saved, err := readMigrationWriter(root, migrationWriterName(copy.TaskID, phase))
	if err == nil && saved.Identity.Copy != copy {
		err = errors.New("migration phase belongs to another original copy")
	}
	return saved, err
}

func requireOwnershipForWriter(root *os.Root, identity MigrationWriterIdentity) error {
	if identity.Copy.WriterVersion != 2 || identity.Phase == "copy" {
		return nil
	}
	phase, _, _ := strings.Cut(identity.Phase, "-")
	_, err := ReadMigrationOwnership(root, identity.Copy, phase)
	return err
}
