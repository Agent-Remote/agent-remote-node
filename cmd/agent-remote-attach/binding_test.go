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

func TestExactBindingCommandRejectsChangedServerAttempt(t *testing.T) {
	for _, returned := range []string{"bind-original", "bind-replacement", ""} {
		t.Run("returned_"+returned, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/v1/node-api/binding-attach/verify" || r.Header.Get("Authorization") != "Bearer synthetic-node-token" {
					t.Errorf("unexpected authenticated endpoint")
				}
				var body api.VerifyBindingAttachRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.ToolAccountID != "11111111-1111-4111-8111-111111111111" || body.ExpectedBindingSessionID != "bind-original" || body.DeviceID != "device-1" || body.SSHKeyID != "key-1" {
					t.Errorf("request identity drift: %#v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
					"binding_session_id": returned, "runtime_backend": "native", "tmux_session_name": "binding-original", "ssh_key_id": "key-1",
				}})
			}))
			defer server.Close()
			path := bindingTestConfig(t, server.URL)
			t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-attach --binding 11111111-1111-4111-8111-111111111111 --binding-session bind-original")
			err := run([]string{"--config", path, "--device", "device-1", "--ssh-key", "key-1", "--dry-run"})
			if (err == nil) != (returned == "bind-original") {
				t.Fatalf("returned %q: %v", returned, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("unexpected authorization requests: %d", calls.Load())
			}
		})
	}
}

func TestExactBindingRequiresForcedCommandKeyIdentity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected request", http.StatusForbidden)
	}))
	defer server.Close()
	path := bindingTestConfig(t, server.URL)
	t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-attach --binding account --binding-session bind-original")
	err := run([]string{"--config", path, "--device", "device-1", "--dry-run"})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("missing forced-command key was not rejected locally: %v, requests=%d", err, calls.Load())
	}
}

func TestExactBindingRejectsServerWithoutKeyVerification(t *testing.T) {
	for _, key := range []string{"", "different-key"} {
		t.Run(key, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{
					"binding_session_id": "bind-original", "runtime_backend": "native",
					"tmux_session_name": "binding-original", "ssh_key_id": key,
				}})
			}))
			defer server.Close()
			path := bindingTestConfig(t, server.URL)
			t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-attach --binding account --binding-session bind-original")
			err := run([]string{"--config", path, "--device", "device-1", "--ssh-key", "key-1", "--dry-run"})
			if err == nil {
				t.Fatal("unverified SSH key was accepted")
			}
		})
	}
}

func TestMalformedExactBindingNeverFallsThroughToSync(t *testing.T) {
	for _, command := range []string{
		"agent-remote-attach --binding account --binding-session",
		"agent-remote-attach --binding account --binding-session ../other",
		"agent-remote-attach --binding account --binding-session " + strings.Repeat("a", 129),
		"agent-remote-attach --binding account --binding-session bind;id",
		"agent-remote-attach --binding account --binding-session bind extra",
		"agent-remote-attach --session account --binding-session bind",
	} {
		t.Run(command, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "unexpected request", http.StatusForbidden)
			}))
			defer server.Close()
			path := bindingTestConfig(t, server.URL)
			t.Setenv("SSH_ORIGINAL_COMMAND", command)
			if err := run([]string{"--config", path, "--device", "device-1", "--dry-run"}); err == nil {
				t.Fatal("malformed attach accepted")
			}
			if calls.Load() != 0 {
				t.Fatalf("malformed command reached server: %d", calls.Load())
			}
		})
	}
}

func bindingTestConfig(t *testing.T, server string) string {
	t.Helper()
	cfg := config.Config{ServerURL: server, NodeID: "node-1", NodeToken: "synthetic-node-token", AllowedRuntimeBackends: []string{"native"}}.WithDefaults()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProtocolInfoIsIsolatedFromSSHAndConfiguration(t *testing.T) {
	t.Setenv("SSH_ORIGINAL_COMMAND", "agent-remote-attach --binding account --binding-session attempt")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	original := os.Stdout
	os.Stdout = w
	err = run([]string{"--protocol-info"})
	os.Stdout = original
	_ = w.Close()
	if err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "{\"supported\":true,\"protocol_version\":1}\n" {
		t.Fatalf("unexpected gateway capability: %q", output)
	}
	if err := run([]string{"--protocol-info", "--device", "untrusted"}); err == nil {
		t.Fatal("protocol probe accepted operational arguments")
	}
}
