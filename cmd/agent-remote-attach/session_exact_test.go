package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
)

const exactTestNode = "11111111-1111-4111-8111-111111111111"
const exactTestSession = "22222222-2222-4222-8222-222222222222"
const exactTestDevice = "33333333-3333-4333-8333-333333333333"
const exactTestKey = "44444444-4444-4444-8444-444444444444"

func exactSessionConfig(t *testing.T, server string) string {
	t.Helper()
	cfg := config.Config{ServerURL: server, NodeID: exactTestNode, NodeToken: "synthetic-node-token", AllowedRuntimeBackends: []string{"native", "docker_sandbox"}}.WithDefaults()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExactSessionGatewayVerifiesActualDeviceAndKey(t *testing.T) {
	for _, backend := range []string{"native", "docker_sandbox"} {
		for _, forward := range []bool{false, true} {
			t.Run(backend+"_"+map[bool]string{true: "forward", false: "no_forward"}[forward], func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != "/api/v1/node-api/attach/exact/verify" {
						t.Error("legacy fallback")
					}
					var body api.VerifyExactAttachRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body != (api.VerifyExactAttachRequest{NodeID: exactTestNode, SessionID: exactTestSession, DeviceID: exactTestDevice, SSHKeyID: exactTestKey}) {
						t.Errorf("identity drift: %#v", body)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1, "tool_type": "claude", "node_id": exactTestNode, "session_id": exactTestSession, "device_id": exactTestDevice, "ssh_key_id": exactTestKey, "runtime_backend": backend, "tmux_session_name": "tool-exact", "container_id": nil, "runtime_resource_id": nil, "forward_ssh_agent": forward}})
				}))
				defer server.Close()
				path := exactSessionConfig(t, server.URL)
				t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-attach --session "+exactTestSession+" --expected-device "+exactTestDevice)
				t.Setenv("SSH_AUTH_SOCK", "/synthetic/agent.sock")
				output, err := captureSessionOutput(t, func() error {
					return run([]string{"--config", path, "--device", exactTestDevice, "--ssh-key", exactTestKey, "--dry-run"})
				})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(output, "attach --session "+exactTestSession+" --runtime-backend "+backend) || strings.Contains(output, "--ssh-agent-sock") != forward {
					t.Fatalf("dispatch drift: %q", output)
				}
				if calls.Load() != 1 {
					t.Fatalf("calls=%d", calls.Load())
				}
			})
		}
	}
}

func TestExactSessionRejectsWrongDeviceAndUntrustedCommandsLocally(t *testing.T) {
	valid := "agent-remote-attach --session " + exactTestSession + " --expected-device " + exactTestDevice
	for _, test := range []struct{ name, command, device, key string }{
		{"wrong_device", valid, exactTestKey, exactTestKey},
		{"missing_key", valid, exactTestDevice, ""},
		{"bad_key", valid, exactTestDevice, "key-1"},
		{"short_session", "agent-remote-attach --session 2222 --expected-device " + exactTestDevice, exactTestDevice, exactTestKey},
		{"null_session", "agent-remote-attach --session 00000000-0000-0000-0000-000000000000 --expected-device " + exactTestDevice, exactTestDevice, exactTestKey},
		{"missing_expected", valid[:len(valid)-36], exactTestDevice, exactTestKey},
		{"injected_key", valid + " --ssh-key " + exactTestKey, exactTestDevice, exactTestKey},
		{"binding_device", "agent-remote-attach --binding " + exactTestSession + " --expected-device " + exactTestDevice, exactTestDevice, exactTestKey},
		{"shell", valid + ";id", exactTestDevice, exactTestKey},
		{"newline", valid + "\n", exactTestDevice, exactTestKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer server.Close()
			path := exactSessionConfig(t, server.URL)
			t.Setenv("SSH_ORIGINAL_COMMAND", test.command)
			if err := run([]string{"--config", path, "--device", test.device, "--ssh-key", test.key, "--dry-run"}); err == nil {
				t.Fatal("unsafe command accepted")
			}
			if calls.Load() != 0 {
				t.Fatalf("unsafe command reached server: %d", calls.Load())
			}
		})
	}
}

func TestExactSessionGatewayRejectsWrongEchoBeforeDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": 1, "tool_type": "claude", "node_id": exactTestNode, "session_id": exactTestSession, "device_id": exactTestDevice, "ssh_key_id": exactTestDevice, "runtime_backend": "native", "tmux_session_name": "tool-exact", "container_id": nil, "runtime_resource_id": nil, "forward_ssh_agent": false}})
	}))
	defer server.Close()
	path := exactSessionConfig(t, server.URL)
	t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-attach --session "+exactTestSession+" --expected-device "+exactTestDevice)
	output, err := captureSessionOutput(t, func() error {
		return run([]string{"--config", path, "--device", exactTestDevice, "--ssh-key", exactTestKey, "--dry-run"})
	})
	if err == nil || output != "" {
		t.Fatalf("unverified key dispatched: %q %v", output, err)
	}
}

func TestSessionProtocolInfoIsIsolatedFromCredentials(t *testing.T) {
	t.Setenv("AGENT_REMOTE_NODE_CONFIG", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("SSH_ORIGINAL_COMMAND", "untrusted command")
	output, err := captureSessionOutput(t, func() error { return run([]string{"--session-protocol-info"}) })
	if err != nil || output != "{\"supported\":true,\"protocol_version\":1}\n" {
		t.Fatalf("probe: %q %v", output, err)
	}
	if err := run([]string{"--session-protocol-info", "--device", exactTestDevice}); err == nil {
		t.Fatal("probe accepted extra arguments")
	}
}

func captureSessionOutput(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	original := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = original; _ = w.Close() }()
	runErr := fn()
	os.Stdout = original
	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}
