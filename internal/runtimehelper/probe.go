package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (e Engine) probe(parent context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	_, dockerIdentityError := e.dockerRuntimeIdentity()
	nativeChecks := map[string]bool{
		"linux":           runtime.GOOS == "linux",
		"kernel_5_15":     kernelAtLeast(5, 15),
		"root":            os.Geteuid() == 0,
		"cgroup_v2":       pathExists("/sys/fs/cgroup/cgroup.controllers"),
		"bwrap":           commandAvailable(e.config.BubblewrapPath),
		"bwrap_self_test": probeCommandSucceeds(ctx, e.config.BubblewrapPath, "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--unshare-user", "true"),
		"systemd_run":     commandAvailable(e.config.SystemdRunPath),
		"systemd_249":     probeSystemdAtLeast(ctx, e.config.SystemdRunPath, 249),
		"ip":              commandAvailable(e.config.IPPath),
		"nft":             commandAvailable(e.config.NFTPath),
		"setfacl":         commandAvailable(e.config.SetfaclPath),
		"mount":           commandAvailable(e.config.MountPath),
		"umount":          commandAvailable(e.config.UmountPath),
		"mountpoint":      commandAvailable(e.config.MountpointPath),
		"tmux":            commandAvailable(e.config.TmuxBinaryPath),
		"git":             commandAvailable("git"),
		"gh":              commandAvailable("gh"),
		"ssh_client":      commandAvailable("ssh"),
		"claude_runtime":  executableExists(e.config.ClaudeRuntimePath),
		"nodejs_runtime":  executableExists(filepath.Join(filepath.Dir(e.config.ClaudeRuntimePath), "node")),
		"locale":          probeLocaleAvailable(ctx, "en_US.UTF-8"),
		"network_ns":      pathExists("/proc/self/ns/net"),
		"tun":             pathExists("/dev/net/tun"),
		"disk_watermark":  diskAvailableAt(e.config.StateRoot, 2<<30),
	}
	nativeOK := true
	for _, available := range nativeChecks {
		nativeOK = nativeOK && available
	}
	dockerChecks := map[string]bool{
		"systemd_run": commandAvailable(e.config.SystemdRunPath),
		"systemd_249": probeSystemdAtLeast(ctx, e.config.SystemdRunPath, 249),
		"systemctl":   commandAvailable(e.config.SystemctlPath),
		"useradd":     commandAvailable("useradd"),

		"linux":            runtime.GOOS == "linux",
		"root":             os.Geteuid() == 0,
		"docker":           commandAvailable(e.config.DockerBinaryPath),
		"daemon":           probeCommandSucceeds(ctx, e.config.DockerBinaryPath, "info"),
		"docker_sandbox":   checkDockerSandboxCommands(ctx, e.config.DockerBinaryPath),
		"tmux":             commandAvailable(e.config.TmuxBinaryPath),
		"git":              commandAvailable("git"),
		"setfacl":          commandAvailable(e.config.SetfaclPath),
		"runtime_identity": dockerIdentityError == nil,
	}
	dockerOK := true
	for _, available := range dockerChecks {
		dockerOK = dockerOK && available
	}
	backends := []string{}
	if dockerOK {
		backends = append(backends, "docker_sandbox")
	}
	if nativeOK {
		backends = append(backends, "native")
	}
	details := probeDependencyDetails(ctx, e.config)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	skillReports, skillChecks := e.probeManagedSkills(ctx, nativeOK)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"available": nativeOK || dockerOK, "backends": backends,
		"native": nativeChecks, "docker_sandbox": dockerChecks,
		"browser_docker": map[string]bool{"docker": dockerChecks["docker"], "daemon": dockerChecks["daemon"]},
		"dependencies":   details,
		"skill_manager":  skillReports, "skill_manager_checks": skillChecks,
	}, nil
}

func probeDependencyDetails(ctx context.Context, config EngineConfig) map[string]string {
	details := map[string]string{
		"kernel":  probeFirstCommandLine(ctx, "uname", "-r"),
		"systemd": probeFirstCommandLine(ctx, config.SystemdRunPath, "--version"),
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				details["distribution"] = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}
	runtimeRoot := filepath.Dir(filepath.Dir(config.ClaudeRuntimePath))
	for key, name := range map[string]string{
		"claude_version": "VERSION", "claude_checksum": "SHA256SUMS",
		"nodejs_version": "NODE_VERSION", "nodejs_checksum": "NODE_SHA256SUMS",
	} {
		if data, err := os.ReadFile(filepath.Join(runtimeRoot, name)); err == nil {
			details[key] = strings.TrimSpace(string(data))
		}
	}
	return details
}

func probeFirstCommandLine(ctx context.Context, binary string, args ...string) string {
	output, err := runProbeCommand(ctx, binary, args...)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
	return line
}

func probeSystemdAtLeast(ctx context.Context, binary string, minimum int) bool {
	output, err := runProbeCommand(ctx, binary, "--version")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 {
		return false
	}
	version, err := strconv.Atoi(fields[1])
	return err == nil && version >= minimum
}

func probeLocaleAvailable(ctx context.Context, locale string) bool {
	output, err := runProbeCommand(ctx, "locale", "-a")
	if err != nil {
		return false
	}
	wanted := normalizeLocale(locale)
	for _, available := range strings.Fields(string(output)) {
		if normalizeLocale(available) == wanted {
			return true
		}
	}
	return false
}

func probeCommandSucceeds(ctx context.Context, binary string, args ...string) bool {
	_, err := runProbeCommand(ctx, binary, args...)
	return err == nil
}

func runProbeCommand(ctx context.Context, binary string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	output := &boundedUnitOutput{remaining: 64 << 10}
	command.Stdout, command.Stderr = output, io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = 100 * time.Millisecond
	err := command.Run()
	if err != nil || output.overflow || ctx.Err() != nil {
		if command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		return "", errors.New("runtime dependency probe failed")
	}
	return output.buffer.String(), nil
}
