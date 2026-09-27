package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/tmuxsession"
)

var nativePaneIDPattern = regexp.MustCompile(`^%[0-9]{1,20}$`)

func superviseManagedNative(ctx context.Context, config EngineConfig, spec SessionSpec, command string) error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	defer signal.Stop(stop)
	args := tmuxsession.NewSessionArgs(config.TmuxBinaryPath, spec.TmuxSocketPath, spec.TmuxSessionName, command)
	program := args[len(args)-1]
	args = append(args[:len(args)-1], "-P", "-F", "#{pane_id}", program)
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	cmd := exec.CommandContext(startCtx, config.TmuxBinaryPath, args...)
	cmd.Dir = spec.WorkspacePath
	cmd.Env = withEgoBrowserEnvironment(replaceEnvironment(os.Environ(), "SHELL", "/bin/sh"), spec, false)
	output := &boundedUnitOutput{remaining: 4096}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	err := cmd.Run()
	cancel()
	pane := strings.TrimSpace(output.buffer.String())
	if err != nil || output.overflow || !nativePaneIDPattern.MatchString(pane) {
		return errors.New("managed tmux did not return the original pane identity")
	}
	// The initial pane waits for the first client; retention is set before enabling its attach hook.
	if _, err := nativeTmuxCommand(ctx, config, spec, "set-window-option", "-t", pane, "remain-on-exit", "on"); err != nil {
		return err
	}
	if err := tmuxsession.Configure(config.TmuxBinaryPath, spec.TmuxSocketPath, spec.TmuxSessionName); err != nil {
		return err
	}
	return waitManagedNativePane(ctx, config, spec, pane, stop)
}

func waitManagedNativePane(ctx context.Context, config EngineConfig, spec SessionSpec, pane string, stop <-chan os.Signal) error {
	if !nativePaneIDPattern.MatchString(pane) {
		return errors.New("invalid managed pane identity")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var interruptedAt time.Time
	var exitWait nativePaneExitWait
	var reapRequestedAt time.Time
	secondInterrupt := false
	for {
		output, err := nativeTmuxCommand(ctx, config, spec, "display-message", "-p", "-t", pane, "#{pane_dead}|#{pane_dead_status}")
		dead := false
		if err == nil {
			dead, err = exitWait.inspect(output, time.Now())
			if err == nil && !exitWait.started.IsZero() && !dead && time.Since(reapRequestedAt) >= 200*time.Millisecond {
				// Some tmux versions leave the pane zombie until another child event wakes reaping.
				// This fixed no-op cannot supply an exit status; the next observation still must do so.
				_, err = nativeTmuxCommand(ctx, config, spec, "run-shell", "-b", "-t", pane, "/bin/true")
				reapRequestedAt = time.Now()
			}
		}
		if dead || err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, cleanupErr := nativeTmuxCommand(cleanupCtx, config, spec, "kill-session", "-t", spec.TmuxSessionName)
			cancel()
			if dead && cleanupErr == nil && spec.BootID != "" {
				if markerErr := os.WriteFile(processExitMarkerPath(spec), []byte(spec.BootID+"\n"), 0o600); markerErr != nil {
					return errors.Join(err, markerErr)
				}
			}
			return errors.Join(err, cleanupErr)
		}
		select {
		case <-ctx.Done():
			// The next bounded inspection takes the common cleanup path on cancellation.
		case <-stop:
			if interruptedAt.IsZero() && exitWait.started.IsZero() {
				if _, err := nativeTmuxCommand(ctx, config, spec, "send-keys", "-t", pane, "C-c"); err != nil {
					return err
				}
				interruptedAt = time.Now()
			}
		case <-ticker.C:
			// Claude may use the first interrupt to cancel a turn and a second to exit.
			if !interruptedAt.IsZero() && !secondInterrupt && exitWait.started.IsZero() && time.Since(interruptedAt) >= time.Second {
				if _, err := nativeTmuxCommand(ctx, config, spec, "send-keys", "-t", pane, "C-c"); err != nil {
					return err
				}
				secondInterrupt = true
			}
		}
	}
}

func nativeTmuxCommand(ctx context.Context, config EngineConfig, spec SessionSpec, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, config.TmuxBinaryPath, append([]string{"-S", spec.TmuxSocketPath}, args...)...)
	output := &boundedUnitOutput{remaining: 4096}
	command.Stdout, command.Stderr = output, io.Discard
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("managed tmux inspection or cleanup failed: %w", err)
	}
	if output.overflow {
		return "", errors.New("managed tmux response exceeds inspection limit")
	}
	return strings.TrimSpace(output.buffer.String()), nil
}

func parseNativePaneExit(output string) (bool, error) {
	dead, status, found := strings.Cut(output, "|")
	if !found || strings.ContainsAny(status, "|\r\n") {
		return false, errors.New("managed pane exit state is malformed")
	}
	if dead == "0" && (status == "" || status == "0") {
		return false, nil
	}
	if dead != "1" {
		return false, errors.New("managed pane exit state is unknown")
	}
	exitCode, err := strconv.ParseUint(status, 10, 8)
	if err != nil || strconv.FormatUint(exitCode, 10) != status {
		return true, errors.New("managed pane exited without a normal exit status")
	}
	if exitCode != 0 {
		return true, errors.New("managed tool exited unsuccessfully")
	}
	return true, nil
}

func (e Engine) nativeSessionActive(spec SessionSpec) (bool, error) {
	if spec.SkillSnapshotID == "" {
		return exec.Command(e.config.SystemctlPath, "is-active", "--quiet", spec.UnitName).Run() == nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := readNativeUnit(ctx, e.config.SystemctlPath, spec.UnitName)
	if err != nil {
		return false, err
	}
	if state.SubState != "exited" && state.ActiveState != "inactive" && state.ActiveState != "failed" {
		return true, nil
	}
	group := state.ControlGroup
	if group == "" {
		group = "/system.slice/" + spec.UnitName
	}
	if !validSessionCgroup(group, spec.UnitName) {
		return false, errors.New("native session has an unexpected control group")
	}
	if err := confirmEmptyCgroup(e.config.CgroupRoot, group); err != nil {
		return false, err
	}
	return false, nil
}
