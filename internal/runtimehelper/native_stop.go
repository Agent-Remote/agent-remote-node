package runtimehelper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type nativeTermination struct {
	Unclean bool
}

type nativeUnitState struct {
	LoadState       string
	ActiveState     string
	SubState        string
	ControlGroup    string
	Result          string
	ExitCode        string
	ExitStatus      string
	InvocationID    string
	Transient       string
	User            string
	Description     string
	Restart         string
	RemainAfterExit string
}

func (e Engine) stopNativeWriters(ctx context.Context, spec SessionSpec) (nativeTermination, error) {
	spec, invocation, err := e.managedNativeStopAuthority(ctx, spec)
	if err != nil {
		return nativeTermination{}, err
	}
	return e.stopNativeWritersWithInvocation(ctx, spec, invocation)
}

func (e Engine) stopNativeWritersWithInvocation(ctx context.Context, spec SessionSpec, invocation string) (nativeTermination, error) {
	if spec.SkillSnapshotID != "" && (!validSkillUUID(spec.BootID) || spec.BootID != currentBootID()) {
		return nativeTermination{}, errors.New("previous-boot skill runtime requires passive recovery")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	before, err := e.readNativeStopUnit(ctx, spec, invocation)
	if err != nil {
		return nativeTermination{}, err
	}
	group := before.ControlGroup
	if group == "" {
		group = "/system.slice/" + spec.UnitName
	}
	if !validSessionCgroup(group, spec.UnitName) {
		return nativeTermination{}, errors.New("native session has an unexpected control group")
	}
	if spec.SkillSnapshotID != "" && before.ActiveState == "active" && before.SubState == "running" {
		before = e.requestNativeGracefulExit(ctx, spec, before, invocation)
		// A failed graceful inspection is not authority to stop a replacement invocation.
		if invocation != "" {
			before, err = e.readNativeStopUnit(ctx, spec, invocation)
			if err != nil {
				return nativeTermination{}, err
			}
		}
		if before.ControlGroup != "" && before.ControlGroup != group {
			return nativeTermination{}, errors.New("native session control group changed during graceful stop")
		}
	}
	completedBeforeStop := before.SubState == "exited" || before.ActiveState == "inactive" || before.ActiveState == "failed"
	cleanBeforeStop := completedBeforeStop && successfulNativeExit(before) && confirmEmptyCgroup(e.config.CgroupRoot, group) == nil
	if before.LoadState != "not-found" {
		command := exec.CommandContext(ctx, e.config.SystemctlPath, "stop", spec.UnitName)
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if err := command.Run(); err != nil {
			return nativeTermination{}, fmt.Errorf("native session stop failed; preserving session state: %w", err)
		}
	}
	after, err := e.readNativeStopUnit(ctx, spec, invocation)
	if err != nil {
		return nativeTermination{}, err
	}
	if after.ActiveState != "inactive" && after.ActiveState != "failed" {
		return nativeTermination{}, errors.New("native session is not stopped; preserving session state")
	}
	if after.ControlGroup != "" && after.ControlGroup != group {
		return nativeTermination{}, errors.New("native session control group changed while stopping")
	}
	if err := confirmEmptyCgroup(e.config.CgroupRoot, group); err != nil {
		return nativeTermination{}, err
	}
	if err := ctx.Err(); err != nil {
		return nativeTermination{}, err
	}
	clean := cleanBeforeStop
	if spec.SkillSnapshotID == "" {
		// Legacy stop results retain their prior meaning without authorizing skill publication.
		clean = clean || !completedBeforeStop && successfulNativeExit(after)
	}
	// Forced cleanup can kill remaining writers even when the supervisor exits with status zero.
	// Only pre-stop normal exit and whole-cgroup quiescence certify managed input as clean.
	return nativeTermination{Unclean: spec.BootID == "" || spec.BootID != currentBootID() || !clean}, nil
}

func (e Engine) readNativeStopUnit(ctx context.Context, spec SessionSpec, invocation string) (nativeUnitState, error) {
	if invocation == "" {
		return readNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	}
	state, err := readManagedNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err == nil {
		err = requireRetainedInvocation(state, spec, invocation)
	}
	return state, err
}

func (e Engine) requestNativeGracefulExit(ctx context.Context, spec SessionSpec, previous nativeUnitState, invocation string) nativeUnitState {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, e.config.SystemctlPath, "kill", "--kill-whom=main", "--signal=SIGTERM", spec.UnitName)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		if state, err := e.readNativeStopUnit(ctx, spec, invocation); err == nil {
			return state
		}
		return previous
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := e.readNativeStopUnit(ctx, spec, invocation)
		if err != nil {
			return previous
		}
		previous = state
		if state.SubState == "exited" || state.ActiveState == "inactive" || state.ActiveState == "failed" {
			group := state.ControlGroup
			if group == "" {
				group = "/system.slice/" + spec.UnitName
			}
			if !validSessionCgroup(group, spec.UnitName) || confirmEmptyCgroup(e.config.CgroupRoot, group) == nil {
				return state
			}
		}
		select {
		case <-ctx.Done():
			return previous
		case <-ticker.C:
		}
	}
}

func successfulNativeExit(state nativeUnitState) bool {
	return state.LoadState == "loaded" && state.Result == "success" && state.ExitCode == "1" && state.ExitStatus == "0"
}

func validSessionCgroup(group, unit string) bool {
	return filepath.IsAbs(group) && filepath.Clean(group) == group && filepath.Base(group) == unit && unit != "" && !strings.ContainsAny(group, "\x00\r\n")
}

func readNativeUnit(ctx context.Context, systemctl, unit string) (nativeUnitState, error) {
	return readNativeUnitState(ctx, systemctl, unit, false)
}

func readManagedNativeUnit(ctx context.Context, systemctl, unit string) (nativeUnitState, error) {
	return readNativeUnitState(ctx, systemctl, unit, true)
}

func readNativeUnitState(ctx context.Context, systemctl, unit string, managed bool) (nativeUnitState, error) {
	return readUnitState(ctx, systemctl, unit, managed, false)
}

func readUnitState(ctx context.Context, systemctl, unit string, managed, migration bool) (nativeUnitState, error) {
	properties := "LoadState,ActiveState,SubState,ControlGroup,Result,ExecMainCode,ExecMainStatus"
	if managed {
		properties += ",InvocationID,Transient,User"
	}
	if migration {
		properties += ",Description,Restart,RemainAfterExit"
	}
	command := exec.CommandContext(ctx, systemctl, "show", "--no-pager", "--property="+properties, unit)
	output := &boundedUnitOutput{remaining: 16 * 1024}
	command.Stdout, command.Stderr = output, io.Discard
	commandErr := command.Run()
	if ctx.Err() != nil {
		return nativeUnitState{}, ctx.Err()
	}
	if output.overflow {
		return nativeUnitState{}, errors.New("native unit state exceeds inspection limit")
	}
	fields := make(map[string]string)
	required := map[string]bool{"LoadState": true, "ActiveState": true, "SubState": true, "ControlGroup": true, "Result": true, "ExecMainCode": true, "ExecMainStatus": true}
	if managed {
		required["InvocationID"], required["Transient"], required["User"] = true, true, true
	}
	if migration {
		required["Description"], required["Restart"], required["RemainAfterExit"] = true, true, true
	}
	for _, line := range strings.Split(strings.TrimSpace(output.buffer.String()), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !required[key] {
			return nativeUnitState{}, errors.New("native unit inspection returned malformed state")
		}
		if _, duplicate := fields[key]; duplicate {
			return nativeUnitState{}, errors.New("native unit inspection returned duplicate fields")
		}
		fields[key] = value
	}
	state := nativeUnitState{LoadState: fields["LoadState"], ActiveState: fields["ActiveState"], SubState: fields["SubState"], ControlGroup: fields["ControlGroup"], Result: fields["Result"], ExitCode: fields["ExecMainCode"], ExitStatus: fields["ExecMainStatus"]}
	state.InvocationID, state.Transient, state.User = fields["InvocationID"], fields["Transient"], fields["User"]
	state.Description, state.Restart, state.RemainAfterExit = fields["Description"], fields["Restart"], fields["RemainAfterExit"]
	if state.LoadState != "loaded" && state.LoadState != "not-found" {
		return nativeUnitState{}, errors.New("native unit load state is unknown")
	}
	if state.LoadState == "loaded" && (len(fields) != len(required) || state.SubState == "") {
		return nativeUnitState{}, errors.New("native unit inspection is incomplete")
	}
	switch state.ActiveState {
	case "active", "reloading", "inactive", "failed", "activating", "deactivating", "maintenance", "refreshing":
	default:
		return nativeUnitState{}, errors.New("native unit active state is unknown")
	}
	if state.LoadState == "not-found" && (state.ActiveState != "inactive" || state.ControlGroup != "") {
		return nativeUnitState{}, errors.New("native unit absence contradicts its runtime state")
	}
	if commandErr != nil && state.LoadState != "not-found" {
		return nativeUnitState{}, fmt.Errorf("inspect native unit: %w", commandErr)
	}
	if ctx.Err() != nil {
		return nativeUnitState{}, ctx.Err()
	}
	return state, nil
}

func confirmEmptyCgroup(rootPath, group string) error {
	if !filepath.IsAbs(group) || filepath.Clean(group) != group || group == "/" {
		return errors.New("invalid native cgroup path")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return errors.New("cannot inspect native control groups; preserving session state")
	}
	defer root.Close()
	file, err := root.Open(filepath.Join(strings.TrimPrefix(group, "/"), "cgroup.events"))
	if errors.Is(err, os.ErrNotExist) {
		// Missing events alone is not proof: only removal of the entire cgroup proves it empty.
		if _, statErr := root.Stat(strings.TrimPrefix(group, "/")); errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return errors.New("cannot inspect native cgroup population; preserving session state")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return errors.New("invalid native cgroup state; preserving session state")
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return errors.New("invalid native cgroup event; preserving session state")
		}
		if fields[0] == "populated" {
			if found || fields[1] != "0" {
				return errors.New("native session still has writers; preserving session state")
			}
			found = true
		}
	}
	if !found {
		return errors.New("native cgroup population is unknown; preserving session state")
	}
	return nil
}

// A failed or cancelled start can still have created a live systemd unit. Cleanup uses a
// fresh bounded context because cancellation of the launch is not evidence of writer exit.
func (e Engine) cleanupFailedNativeLaunch(spec SessionSpec) error {
	e.stopDockerCapability(spec.DockerSocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	termination, err := e.stopNativeWriters(ctx, spec)
	if err != nil {
		return err
	}
	termination.Unclean = true
	if result, err := e.finalizeNativeSkillSession(ctx, spec, termination); err != nil {
		return err
	} else if result != nil {
		return errors.New("state_pending: failed launch retained skill data for recovery")
	}
	if err := runCommand(ctx, e.config.IPPath, "netns", "delete", spec.NetworkNamespace); err != nil {
		return err
	}
	if err := e.cleanupTemp(ctx, spec); err != nil {
		return err
	}
	return e.cleanupNativeSkillMount(spec)
}

type boundedUnitOutput struct {
	buffer    bytes.Buffer
	remaining int
	overflow  bool
}

func (o *boundedUnitOutput) Write(data []byte) (int, error) {
	keep := min(len(data), o.remaining)
	_, _ = o.buffer.Write(data[:keep])
	o.remaining -= keep
	o.overflow = o.overflow || keep < len(data)
	return len(data), nil
}
