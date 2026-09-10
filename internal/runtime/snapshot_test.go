package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func TestHostResourcesReportsMemoryAndDisk(t *testing.T) {
	resources := hostResources()
	if resources.MemoryTotalBytes <= 0 || resources.MemoryUsedBytes < 0 {
		t.Fatalf("invalid memory snapshot: %#v", resources)
	}
	if resources.DiskTotalBytes <= 0 || resources.DiskUsedBytes < 0 {
		t.Fatalf("invalid disk snapshot: %#v", resources)
	}
}

func TestStringMapDropsNonStringValues(t *testing.T) {
	result := stringMap(map[string]any{"kernel": "6.8.0", "invalid": true})
	if result["kernel"] != "6.8.0" || len(result) != 1 {
		t.Fatalf("unexpected string map: %#v", result)
	}
}

func TestProbeCapabilitiesAdvertisesFeaturesForBothRuntimeBackends(t *testing.T) {
	proxyPath := filepath.Join(t.TempDir(), "agent-remote-device-proxy")
	if err := os.WriteFile(proxyPath, []byte("managed proxy"), 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath, done := serveRuntimeProbe(t, map[string]any{
		"backends":       []string{"native", "docker_sandbox"},
		"native":         map[string]bool{"network_ns": true, "tmux": true},
		"docker_sandbox": map[string]bool{"docker": true, "daemon": true},
		"browser_docker": map[string]bool{"docker": true},
		"dependencies":   map[string]string{"kernel": "6.8.0"},
	})
	capabilities := probeCapabilities([]string{"native", "docker_sandbox"}, socketPath, proxyPath)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !capabilities.SessionPortForwarding.Supported {
		t.Fatalf("session port forwarding was not advertised: %#v", capabilities)
	}
	if !slices.Equal(capabilities.SessionPortForwarding.Backends, []string{"native", "docker_sandbox"}) {
		t.Fatalf("incomplete backend capability: %#v", capabilities.SessionPortForwarding)
	}
	if capabilities.SessionPortForwarding.MaxStreams != 128 {
		t.Fatalf("unexpected max streams: %#v", capabilities.SessionPortForwarding)
	}
	if !capabilities.DeviceControl.Supported {
		t.Fatalf("managed device proxy was not advertised: %#v", capabilities.DeviceControl)
	}
	if len(capabilities.DeviceControl.Platforms) != 1 || capabilities.DeviceControl.Platforms[0] != "macos" {
		t.Fatalf("unexpected device platforms: %#v", capabilities.DeviceControl)
	}
	if !slices.Equal(capabilities.DeviceControl.Backends, []string{"native", "docker_sandbox"}) {
		t.Fatalf("incomplete device-control backends: %#v", capabilities.DeviceControl)
	}
	if !slices.Equal(capabilities.DeviceControl.Capabilities, deviceControlCapabilitiesV2) {
		t.Fatalf("unexpected device-control capabilities: %#v", capabilities.DeviceControl)
	}
}

func TestProbeCapabilitiesFailsClosedWithoutNativeNetworkNamespace(t *testing.T) {
	socketPath, done := serveRuntimeProbe(t, map[string]any{
		"backends": []string{"native"},
		"native":   map[string]bool{"network_ns": false},
	})
	capabilities := probeCapabilities([]string{"native"}, socketPath, filepath.Join(t.TempDir(), "missing-proxy"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if capabilities.SessionPortForwarding.Supported || len(capabilities.SessionPortForwarding.Backends) != 0 {
		t.Fatalf("port forwarding must fail closed: %#v", capabilities.SessionPortForwarding)
	}
	if len(capabilities.SessionPortForwarding.ProtocolVersions) != 0 {
		t.Fatalf("disabled capability exposed protocol versions: %#v", capabilities.SessionPortForwarding)
	}
	if capabilities.DeviceControl.Supported || len(capabilities.DeviceControl.ProtocolVersions) != 0 || len(capabilities.DeviceControl.Platforms) != 0 || len(capabilities.DeviceControl.Capabilities) != 0 {
		t.Fatalf("device control must fail closed: %#v", capabilities.DeviceControl)
	}
}

func TestProbeCapabilitiesKeepsDockerFeaturesWithoutNativeNetworkNamespace(t *testing.T) {
	proxyPath := filepath.Join(t.TempDir(), "agent-remote-device-proxy")
	if err := os.WriteFile(proxyPath, []byte("managed proxy"), 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath, done := serveRuntimeProbe(t, map[string]any{
		"backends":       []string{"native", "docker_sandbox"},
		"native":         map[string]bool{"network_ns": false},
		"docker_sandbox": map[string]bool{"docker": true, "daemon": true},
	})
	capabilities := probeCapabilities([]string{"native", "docker_sandbox"}, socketPath, proxyPath)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := []string{"docker_sandbox"}
	if !slices.Equal(capabilities.SessionPortForwarding.Backends, want) ||
		!slices.Equal(capabilities.DeviceControl.Backends, want) {
		t.Fatalf("Docker features were not retained: %#v", capabilities)
	}
}

func TestProbeEgoBrowserRequiresVerifiedWrapperAndSkill(t *testing.T) {
	config := EgoBrowserProbeConfig{
		Enabled: true, WrapperPath: "/opt/agent-remote/ego-browser/current/bin/ego-browser",
		ProtocolVersion: "ego-browser-bridge-v1", WrapperVersion: egobrowserartifact.PinnedWrapperVersion,
		SkillPath:       "/opt/agent-remote/ego-browser/current/skill/ego-browser",
		SkillVersion:    egobrowserartifact.OfficialSkillVersion,
		SkillTreeSHA256: egobrowserartifact.OfficialSkillTreeSHA256,
		MaxScriptBytes:  1 << 20, MaxExecuteTimeoutMS: 120_000,
	}
	rejected := probeEgoBrowserWithVerifier(config, func(egobrowserartifact.RuntimeConfig) error {
		return errors.New("tampered")
	})
	if rejected.Supported || len(rejected.ProtocolVersions) != 0 {
		t.Fatalf("unverified artifact was advertised: %#v", rejected)
	}
	accepted := probeEgoBrowserWithVerifier(config, func(candidate egobrowserartifact.RuntimeConfig) error {
		if candidate.SkillTreeSHA256 != egobrowserartifact.OfficialSkillTreeSHA256 {
			return errors.New("wrong Skill digest")
		}
		return nil
	})
	if !accepted.Supported || accepted.SkillVersion != egobrowserartifact.OfficialSkillVersion ||
		accepted.SkillTreeSHA256 != egobrowserartifact.OfficialSkillTreeSHA256 {
		t.Fatalf("verified artifact was not advertised: %#v", accepted)
	}
}

func serveRuntimeProbe(t *testing.T, result map[string]any) (string, <-chan error) {
	t.Helper()
	temporary, err := os.CreateTemp("", "agent-remote-probe-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	socketPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer listener.Close()
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer connection.Close()
		var request runtimehelper.Request
		if decodeErr := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); decodeErr != nil {
			done <- decodeErr
			return
		}
		done <- json.NewEncoder(connection).Encode(runtimehelper.Response{
			Version: runtimehelper.ProtocolVersion,
			OK:      true,
			Result:  result,
		})
	}()
	return socketPath, done
}
