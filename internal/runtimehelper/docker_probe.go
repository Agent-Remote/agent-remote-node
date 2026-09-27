package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var errDockerSandboxUnavailable = errors.New("Docker Sandbox lifecycle commands are unavailable")

// A removed plugin may print a deprecation notice and still exit zero. Require each lifecycle
// command's own usage declaration; neither daemon availability nor generic help proves support.
func dockerSandboxAvailable(binary string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return checkDockerSandboxCommands(ctx, binary)
}

func checkDockerSandboxCommands(ctx context.Context, binary string) bool {
	for _, operation := range []string{"create", "exec", "rm"} {
		command := exec.CommandContext(ctx, binary, "sandbox", operation, "--help")
		output := &boundedUnitOutput{remaining: 64 << 10}
		command.Stdout, command.Stderr = output, io.Discard
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
		// Cancel the probe group as well as bounding inherited output pipes.
		command.WaitDelay = time.Second
		if command.Run() != nil || output.overflow || !sandboxCommandUsage(output.buffer.String(), operation) {
			return false
		}
	}
	return ctx.Err() == nil
}

func sandboxCommandUsage(output, operation string) bool {
	usage := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "Usage:" {
			usage = true
			continue
		}
		if usage && line != "" {
			fields := strings.Fields(line)
			return len(fields) >= 3 && fields[0] == "docker" && fields[1] == "sandbox" && fields[2] == operation
		}
	}
	return false
}
