package runtime

import (
	"bufio"
	"context"
	"os"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

// Snapshot captures current node resource and runtime status.
func Snapshot(allowedBackends []string, runtimeSocketPath string) (api.ResourceStatus, api.RuntimeStatus) {
	resources := hostResources()
	capabilities := probeCapabilities(allowedBackends, runtimeSocketPath)
	status := api.RuntimeStatus{
		DockerOK:              capabilities.DockerSandbox["docker"] && capabilities.DockerSandbox["daemon"],
		TmuxOK:                capabilities.Native["tmux"] || capabilities.DockerSandbox["tmux"],
		ActiveSessions:        0,
		ActiveBrowserSessions: 0,
		Containers:            0,
		RuntimeCapabilities:   capabilities,
	}
	return resources, status
}

func probeCapabilities(allowedBackends []string, runtimeSocketPath string) api.RuntimeCapabilities {
	capabilities := api.RuntimeCapabilities{
		Backends:      []string{},
		Native:        map[string]bool{},
		DockerSandbox: map[string]bool{},
		BrowserDocker: map[string]bool{},
		Dependencies:  map[string]string{},
		ProbeErrors:   []string{},
	}
	allowed := make(map[string]bool, len(allowedBackends))
	for _, backend := range allowedBackends {
		allowed[backend] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := runtimehelper.NewClient(runtimeSocketPath).Call(ctx, "heartbeat-probe", "probe", map[string]any{})
	if err != nil {
		capabilities.ProbeErrors = append(capabilities.ProbeErrors, "runtime helper probe failed: "+err.Error())
		return capabilities
	}
	capabilities.Native = boolMap(result["native"])
	capabilities.DockerSandbox = boolMap(result["docker_sandbox"])
	capabilities.BrowserDocker = boolMap(result["browser_docker"])
	capabilities.Dependencies = stringMap(result["dependencies"])
	for _, backend := range textSlice(result["backends"]) {
		if allowed[backend] {
			capabilities.Backends = append(capabilities.Backends, backend)
		}
	}
	return capabilities
}

func hostResources() api.ResourceStatus {
	resources := api.ResourceStatus{}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			resources.CPULoad, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		defer file.Close()
		values := map[string]int64{}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 {
				value, parseErr := strconv.ParseInt(fields[1], 10, 64)
				if parseErr == nil {
					values[strings.TrimSuffix(fields[0], ":")] = value * 1024
				}
			}
		}
		resources.MemoryTotalBytes = values["MemTotal"]
		resources.MemoryUsedBytes = values["MemTotal"] - values["MemAvailable"]
	}
	if resources.MemoryTotalBytes == 0 {
		var mem goruntime.MemStats
		goruntime.ReadMemStats(&mem)
		resources.MemoryUsedBytes = int64(mem.Alloc)
		resources.MemoryTotalBytes = int64(mem.Sys)
	}
	var disk syscall.Statfs_t
	if syscall.Statfs("/", &disk) == nil {
		resources.DiskTotalBytes = int64(disk.Blocks) * int64(disk.Bsize)
		resources.DiskUsedBytes = int64(disk.Blocks-disk.Bavail) * int64(disk.Bsize)
	}
	return resources
}

func boolMap(value any) map[string]bool {
	result := map[string]bool{}
	items, _ := value.(map[string]any)
	for key, value := range items {
		if flag, ok := value.(bool); ok {
			result[key] = flag
		}
	}
	return result
}

func stringMap(value any) map[string]string {
	result := map[string]string{}
	items, _ := value.(map[string]any)
	for key, value := range items {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func textSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && !slices.Contains(result, text) {
			result = append(result, text)
		}
	}
	return result
}
