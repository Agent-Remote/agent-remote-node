package runtime

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"testing"

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

func TestProbeCapabilitiesAdvertisesNativeSessionPortForwarding(t *testing.T) {
	socketPath, done := serveRuntimeProbe(t, map[string]any{
		"backends":       []string{"native", "docker_sandbox"},
		"native":         map[string]bool{"network_ns": true, "tmux": true},
		"docker_sandbox": map[string]bool{"docker": true, "daemon": true},
		"browser_docker": map[string]bool{"docker": true},
		"dependencies":   map[string]string{"kernel": "6.8.0"},
	})
	capabilities := probeCapabilities([]string{"native", "docker_sandbox"}, socketPath)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !capabilities.SessionPortForwarding.Supported {
		t.Fatalf("native port forwarding was not advertised: %#v", capabilities)
	}
	if len(capabilities.SessionPortForwarding.Backends) != 1 || capabilities.SessionPortForwarding.Backends[0] != "native" {
		t.Fatalf("unsafe backend capability: %#v", capabilities.SessionPortForwarding)
	}
	if capabilities.SessionPortForwarding.MaxStreams != 128 {
		t.Fatalf("unexpected max streams: %#v", capabilities.SessionPortForwarding)
	}
}

func TestProbeCapabilitiesFailsClosedWithoutNativeNetworkNamespace(t *testing.T) {
	socketPath, done := serveRuntimeProbe(t, map[string]any{
		"backends": []string{"native"},
		"native":   map[string]bool{"network_ns": false},
	})
	capabilities := probeCapabilities([]string{"native"}, socketPath)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if capabilities.SessionPortForwarding.Supported || len(capabilities.SessionPortForwarding.Backends) != 0 {
		t.Fatalf("port forwarding must fail closed: %#v", capabilities.SessionPortForwarding)
	}
	if len(capabilities.SessionPortForwarding.ProtocolVersions) != 0 {
		t.Fatalf("disabled capability exposed protocol versions: %#v", capabilities.SessionPortForwarding)
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
