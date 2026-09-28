package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

var takeoverUnitPattern = regexp.MustCompile(`^agent-remote-session-[a-f0-9]{12}\.service$`)

// Native proof cannot infer Docker sandbox-wide termination from a tmux/spec status.
// Mixed or unknown backend histories require the separate Docker reconciliation adapter.
func (e Engine) checkNativeAccountWriters(ctx context.Context, binding skillmanager.AccountTakeoverBinding, writers []skillmanager.AccountWriter) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cgroupRoot, err := os.OpenRoot(e.config.CgroupRoot)
	if err != nil {
		return errTakeoverWritersUnknown
	}
	_ = cgroupRoot.Close()
	units := make(map[string]bool)
	for _, writer := range writers {
		if writer.NodeID != binding.NodeID {
			return errTakeoverWritersUnknown
		}
		switch writer.Kind {
		case "session", "binding":
			if writer.RuntimeBackend == nil || *writer.RuntimeBackend != "native" {
				return errTakeoverWritersUnknown
			}
			units["agent-remote-session-"+shortDigest(writer.ResourceID, 12)+".service"] = true
		case "import":
			// Imports execute synchronously under Helper serialization; private intents were checked.
		case "backend":
			store, err := e.openExistingSkillStateRoot()
			if err != nil {
				return errTakeoverWritersUnknown
			}
			err = skillmanager.CheckAccountMigrationWriter(store, binding.NodeID, binding.UserID, binding.AccountID, writer.ResourceID)
			_ = store.Close()
			if err != nil {
				return errTakeoverWritersUnknown
			}
		default:
			return errTakeoverWritersUnknown
		}
	}
	otherUnits, err := e.localNativeTakeoverUnits(binding, units)
	if err != nil {
		return err
	}
	if err := e.rejectUnverifiedDockerWriters(binding); err != nil {
		return err
	}
	listed, err := e.listNativeTakeoverUnits(ctx)
	if err != nil {
		return err
	}
	for _, unit := range listed {
		if !otherUnits[unit] {
			units[unit] = true
		}
	}
	// A removed systemd unit can still leave a populated cgroup; include locally retained groups.
	groups, err := takeoverDirectoryEntries(filepath.Join(e.config.CgroupRoot, "system.slice"))
	if err != nil {
		return err
	}
	for _, entry := range groups {
		if strings.HasPrefix(entry.Name(), "agent-remote-session-") {
			if !entry.IsDir() || !takeoverUnitPattern.MatchString(entry.Name()) {
				return errTakeoverWritersUnknown
			}
			if !otherUnits[entry.Name()] {
				units[entry.Name()] = true
			}
		}
	}
	for unit := range units {
		if err := e.inspectNativeTakeoverUnit(ctx, unit); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (e Engine) localNativeTakeoverUnits(binding skillmanager.AccountTakeoverBinding, units map[string]bool) (map[string]bool, error) {
	root, err := e.openTakeoverRuntimeRoot("sessions")
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, errTakeoverWritersUnknown
	}
	defer root.Close()
	entries, err := takeoverRootEntries(root)
	if err != nil {
		return nil, err
	}
	otherUnits := make(map[string]bool)
	for _, entry := range entries {
		// Binding runtimes use bind-<account>-<attempt>, not a session UUID.
		// Apply the same safe identifier grammar as the trusted runtime spec.
		if !entry.IsDir() || validateID(entry.Name(), "session_id") != nil {
			return nil, errTakeoverWritersUnknown
		}
		spec, err := e.readNativeTakeoverSpec(root, entry.Name())
		if err != nil || spec.Kind != "session" && spec.Kind != "binding" {
			return nil, errTakeoverWritersUnknown
		}
		if spec.UserID == binding.UserID && filepath.Base(spec.AccountPath) == binding.AccountID {
			units[spec.UnitName] = true
		} else if !units[spec.UnitName] {
			otherUnits[spec.UnitName] = true
		}
	}
	return otherUnits, nil
}

func (e Engine) rejectUnverifiedDockerWriters(binding skillmanager.AccountTakeoverBinding) error {
	root, err := e.openTakeoverRuntimeRoot("docker-sessions")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errTakeoverWritersUnknown
	}
	defer root.Close()
	entries, err := takeoverRootEntries(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(id, ".json") {
				return errTakeoverWritersUnknown
			}
			id = strings.TrimSuffix(id, ".json")
		}
		spec, err := e.readDockerTakeoverSpec(root, id)
		if err != nil || spec.UserID == "" || spec.UserID == binding.UserID {
			return errTakeoverWritersUnknown
		}
	}
	return nil
}

func (e Engine) listNativeTakeoverUnits(ctx context.Context) ([]string, error) {
	command := exec.CommandContext(ctx, e.config.SystemctlPath, "list-units", "--all", "--type=service", "--no-pager", "--output=json", "agent-remote-session-*.service")
	output := &boundedUnitOutput{remaining: 1 << 20}
	command.Stdout, command.Stderr = output, io.Discard
	if err := command.Run(); err != nil || output.overflow {
		return nil, errTakeoverWritersUnknown
	}
	var rows []struct {
		Unit string `json:"unit"`
	}
	if rejectDuplicateJSONKeys(output.buffer.Bytes()) != nil {
		return nil, errTakeoverWritersUnknown
	}
	if err := json.Unmarshal(output.buffer.Bytes(), &rows); err != nil || rows == nil || len(rows) > 10_000 {
		return nil, errTakeoverWritersUnknown
	}
	units := make([]string, 0, len(rows))
	seen := make(map[string]bool)
	for _, row := range rows {
		if !takeoverUnitPattern.MatchString(row.Unit) || seen[row.Unit] {
			return nil, errTakeoverWritersUnknown
		}
		units, seen[row.Unit] = append(units, row.Unit), true
	}
	return units, nil
}

func (e Engine) inspectNativeTakeoverUnit(ctx context.Context, unit string) error {
	state, err := readNativeUnit(ctx, e.config.SystemctlPath, unit)
	if err != nil {
		return errTakeoverWritersUnknown
	}
	if state.ActiveState != "inactive" && state.ActiveState != "failed" && !(state.ActiveState == "active" && state.SubState == "exited") {
		return errTakeoverWritersActive
	}
	group := state.ControlGroup
	if group == "" {
		group = "/system.slice/" + unit
	}
	if !validSessionCgroup(group, unit) || confirmEmptyCgroup(e.config.CgroupRoot, group) != nil {
		return errTakeoverWritersUnknown
	}
	return ctx.Err()
}

func takeoverDirectoryEntries(path string) ([]os.DirEntry, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errTakeoverWritersUnknown
	}
	file := os.NewFile(uintptr(fd), "takeover-inventory")
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Uid != 0 || stat.Mode&0o022 != 0 {
		return nil, errTakeoverWritersUnknown
	}
	entries, err := file.ReadDir(10_001)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 10_000 {
		return nil, errTakeoverWritersUnknown
	}
	return entries, nil
}

func takeoverRootEntries(root *os.Root) ([]os.DirEntry, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, errTakeoverWritersUnknown
	}
	defer file.Close()
	entries, err := file.ReadDir(10_001)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 10_000 {
		return nil, errTakeoverWritersUnknown
	}
	return entries, nil
}
