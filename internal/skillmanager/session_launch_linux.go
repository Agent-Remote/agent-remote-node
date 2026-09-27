package skillmanager

import (
	"errors"
	"os"
	"regexp"
)

// SessionLaunch binds one at-most-once launch to its immutable completed spec and original boot.
// Started records preserve historical readiness, never a claim that the process is still alive.
type SessionLaunch struct {
	Version      int               `json:"version"`
	Spec         SessionSpecIntent `json:"spec"`
	BootID       string            `json:"boot_id"`
	UnitName     string            `json:"unit_name"`
	State        string            `json:"state"`
	InvocationID string            `json:"invocation_id"`
}

var launchUnitPattern = regexp.MustCompile(`^agent-remote-session-[a-f0-9]{12}\.service$`)
var invocationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validateSessionLaunch(record SessionLaunch) error {
	if record.Version != 1 || validateSessionSpecIntent(record.Spec) != nil || record.Spec.State != "ready" || !snapshotIDPattern.MatchString(record.BootID) || !launchUnitPattern.MatchString(record.UnitName) {
		return errors.New("invalid managed session launch binding")
	}
	if record.State == "starting" && record.InvocationID == "" || (record.State == "observed" || record.State == "started") && invocationPattern.MatchString(record.InvocationID) && record.InvocationID != "00000000000000000000000000000000" {
		return nil
	}
	return errors.New("invalid managed session launch phase")
}

// ReadSessionLaunch checks the complete original binding and its immutable spec authority.
func ReadSessionLaunch(root *os.Root, expected SessionLaunch) (SessionLaunch, error) {
	if err := validateSessionLaunch(expected); err != nil {
		return SessionLaunch{}, err
	}
	var saved SessionLaunch
	if err := readPrivateJSON(root, "session-launch-"+expected.Spec.Identity.SessionID+".json", 1<<20, &saved); err != nil {
		return saved, err
	}
	if err := validateSessionLaunch(saved); err != nil {
		return saved, err
	}
	identity := saved
	identity.State, identity.InvocationID = expected.State, expected.InvocationID
	if identity != expected {
		return saved, errors.New("managed session launch input changed")
	}
	if err := requireLaunchSpec(root, expected.Spec); err != nil {
		return saved, err
	}
	return saved, nil
}

func requireLaunchSpec(root *os.Root, expected SessionSpecIntent) error {
	spec, err := ReadSessionSpecIntent(root, expected)
	if err != nil || spec != expected {
		return errors.New("managed launch requires the original completed spec")
	}
	_, err = ReadSessionSpecDraft(root, expected)
	return err
}

// BeginSessionLaunch persists intent before any process launch and refuses replacement attempts.
func BeginSessionLaunch(root *os.Root, record SessionLaunch) error {
	if err := validateSessionLaunch(record); err != nil {
		return err
	}
	if record.State != "starting" {
		return errors.New("managed session launch must begin in starting state")
	}
	if err := requireLaunchSpec(root, record.Spec); err != nil {
		return err
	}
	directory, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return writePrivateJSON(root, directory, "session-launch-"+record.Spec.Identity.SessionID+".json", record, true)
}

// ObserveSessionLaunch pins a verified invocation without certifying runtime readiness.
// The caller serializes inspection and publication under the Helper mutation lock.
func ObserveSessionLaunch(root *os.Root, expected SessionLaunch, invocationID string) error {
	saved, err := ReadSessionLaunch(root, expected)
	if err != nil {
		return err
	}
	if saved.InvocationID != "" {
		if saved.InvocationID != invocationID {
			return errors.New("managed session launch observation cannot change invocation")
		}
		return syncDirectory(root, ".")
	}
	saved.State, saved.InvocationID = "observed", invocationID
	return writeSessionLaunch(root, saved)
}

// FinishSessionLaunch seals the exact systemd invocation after the Helper observes readiness.
func FinishSessionLaunch(root *os.Root, expected SessionLaunch, invocationID string) error {
	saved, err := ReadSessionLaunch(root, expected)
	if err != nil {
		return err
	}
	if saved.InvocationID != "" && saved.InvocationID != invocationID {
		return errors.New("managed session launch receipt cannot change invocation")
	}
	if saved.State == "started" {
		return syncDirectory(root, ".")
	}
	saved.State, saved.InvocationID = "started", invocationID
	return writeSessionLaunch(root, saved)
}

func writeSessionLaunch(root *os.Root, saved SessionLaunch) error {
	if err := validateSessionLaunch(saved); err != nil {
		return err
	}
	directory, err := privateBundleFile(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return writePrivateJSON(root, directory, "session-launch-"+saved.Spec.Identity.SessionID+".json", saved, false)
}
