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
	"github.com/Agent-Remote/agent-remote-node/internal/devicecontrol"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

var deviceControlCapabilitiesV2 = devicecontrol.SupportedV2Capabilities()

// EgoBrowserProbeConfig describes the optional remote wrapper installation.
type EgoBrowserProbeConfig struct {
	Enabled             bool
	WrapperPath         string
	ProtocolVersion     string
	WrapperVersion      string
	SkillPath           string
	SkillVersion        string
	SkillTreeSHA256     string
	MaxScriptBytes      int
	MaxExecuteTimeoutMS int
}

// Snapshot captures node status, including optional ego-browser capability metadata.
func Snapshot(allowedBackends []string, runtimeSocketPath string, deviceProxyPath string, egoBrowser ...EgoBrowserProbeConfig) (api.ResourceStatus, api.RuntimeStatus) {
	resources := hostResources()
	capabilities := probeCapabilities(allowedBackends, runtimeSocketPath, deviceProxyPath, egoBrowser...)
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

func probeCapabilities(allowedBackends []string, runtimeSocketPath string, deviceProxyPath string, egoBrowser ...EgoBrowserProbeConfig) api.RuntimeCapabilities {
	capabilities := api.RuntimeCapabilities{
		Backends:         []string{},
		Native:           map[string]bool{},
		DockerSandbox:    map[string]bool{},
		BrowserDocker:    map[string]bool{},
		Dependencies:     map[string]string{},
		ProbeErrors:      []string{},
		EgoBrowserBridge: api.EgoBrowserBridgeCapability{ProtocolVersions: []string{}, Backends: []string{}, RemotePlatform: "linux", LocalPlatform: "macos"},
	}
	if len(egoBrowser) > 0 {
		capabilities.EgoBrowserBridge = probeEgoBrowser(egoBrowser[0])
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
	portForwardBackends := supportedFeatureBackends(capabilities, true)
	if len(portForwardBackends) > 0 {
		capabilities.SessionPortForwarding = api.SessionPortForwardingCapability{
			Supported:        true,
			ProtocolVersions: []int{1},
			Backends:         portForwardBackends,
			MaxStreams:       128,
		}
	} else {
		capabilities.SessionPortForwarding = api.SessionPortForwardingCapability{
			ProtocolVersions: []int{},
			Backends:         []string{},
		}
	}
	deviceControlBackends := supportedFeatureBackends(capabilities, true)
	if len(deviceControlBackends) > 0 && executableFile(deviceProxyPath) {
		capabilities.DeviceControl = api.DeviceControlCapability{
			Supported:        true,
			ProtocolVersions: []int{1},
			Platforms:        []string{"macos"},
			Backends:         deviceControlBackends,
			Capabilities:     append([]string(nil), deviceControlCapabilitiesV2...),
		}
	} else {
		capabilities.DeviceControl = api.DeviceControlCapability{
			ProtocolVersions: []int{},
			Platforms:        []string{},
			Backends:         []string{},
			Capabilities:     []string{},
		}
	}
	if capabilities.EgoBrowserBridge.Supported {
		capabilities.EgoBrowserBridge.Backends = supportedFeatureBackends(capabilities, false)
		capabilities.EgoBrowserBridge.Supported = len(capabilities.EgoBrowserBridge.Backends) > 0
	}
	return capabilities
}

func supportedFeatureBackends(capabilities api.RuntimeCapabilities, requireNativeNetworkNamespace bool) []string {
	backends := make([]string, 0, 2)
	for _, backend := range capabilities.Backends {
		switch backend {
		case "native":
			if !requireNativeNetworkNamespace || capabilities.Native["network_ns"] {
				backends = append(backends, backend)
			}
		case "docker_sandbox":
			backends = append(backends, backend)
		}
	}
	return backends
}

func probeEgoBrowser(config EgoBrowserProbeConfig) api.EgoBrowserBridgeCapability {
	return probeEgoBrowserWithVerifier(config, egobrowserartifact.VerifyPinned)
}

func probeEgoBrowserWithVerifier(config EgoBrowserProbeConfig, verify func(egobrowserartifact.RuntimeConfig) error) api.EgoBrowserBridgeCapability {
	result := api.EgoBrowserBridgeCapability{
		ProtocolVersions: []string{},
		Backends:         []string{},
		RemotePlatform:   "linux",
		LocalPlatform:    "macos",
	}
	if !config.Enabled {
		return result
	}
	if config.ProtocolVersion != "ego-browser-bridge-v1" {
		return result
	}
	if config.WrapperVersion != egobrowserartifact.PinnedWrapperVersion ||
		config.MaxScriptBytes <= 0 || config.MaxExecuteTimeoutMS <= 0 {
		return result
	}
	if err := verify(egobrowserartifact.RuntimeConfig{
		WrapperPath: config.WrapperPath, WrapperVersion: config.WrapperVersion,
		SkillPath: config.SkillPath, SkillVersion: config.SkillVersion, SkillTreeSHA256: config.SkillTreeSHA256,
	}); err != nil {
		return result
	}
	result.Supported = true
	result.ProtocolVersions = []string{config.ProtocolVersion}
	result.WrapperVersion = config.WrapperVersion
	result.SkillVersion = config.SkillVersion
	result.SkillTreeSHA256 = config.SkillTreeSHA256
	result.MaxScriptBytes = config.MaxScriptBytes
	result.MaxExecuteTimeoutMS = config.MaxExecuteTimeoutMS
	return result
}

func executableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
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
