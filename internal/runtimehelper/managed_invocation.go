package runtimehelper

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"strings"
)

// Only the same transient service, runtime identity and canonical cgroup may certify readiness.
func (e Engine) managedLaunchInvocation(ctx context.Context, spec SessionSpec) (string, error) {
	command := exec.CommandContext(ctx, e.config.SystemctlPath, "show", "--no-pager", "--property=InvocationID,Transient,User,ControlGroup", spec.UnitName)
	output := &boundedUnitOutput{remaining: 4096}
	command.Stdout, command.Stderr = output, io.Discard
	if err := command.Run(); err != nil || ctx.Err() != nil || output.overflow {
		return "", errManagedStartPending
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(output.buffer.String()), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return "", errManagedStartPending
		}
		if _, exists := fields[key]; exists {
			return "", errManagedStartPending
		}
		fields[key] = value
	}
	id := fields["InvocationID"]
	if len(fields) != 4 || !validManagedInvocationID(id) || fields["Transient"] != "yes" || fields["User"] != spec.Username || fields["ControlGroup"] != "/system.slice/"+spec.UnitName {
		return "", errors.New("managed service invocation does not match its original runtime")
	}
	return id, nil
}

func validManagedInvocationID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && strings.ToLower(id) == id && id != strings.Repeat("0", 32)
}
