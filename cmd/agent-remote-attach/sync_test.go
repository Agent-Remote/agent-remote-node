package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSyncGatewayBindsTrustedSSHKey(t *testing.T) {
	for _, returned := range []string{"key-1", "", "other-key"} {
		t.Run(returned, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/api/v1/node-api/sync/verify" || r.Header.Get("Authorization") != "Bearer synthetic-node-token" {
					t.Error("wrong authenticated endpoint")
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["node_id"] != "node-1" || body["device_id"] != "device-1" || body["ssh_key_id"] != "key-1" {
					t.Errorf("identity drift: %#v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"user_id": "user-1", "ssh_key_id": returned}})
			}))
			defer server.Close()
			path := bindingTestConfig(t, server.URL)
			t.Setenv("SSH_ORIGINAL_COMMAND", "mutagen-agent synchronizer")
			err := run([]string{"--config", path, "--device", "device-1", "--ssh-key", "key-1", "--dry-run"})
			if (err == nil) != (returned == "key-1") {
				t.Fatalf("echo %q accepted incorrectly: %v", returned, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("requests = %d", calls.Load())
			}
		})
	}
}

func TestSyncGatewayRejectsMissingTrustedKeyBeforeAuthorization(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 403) }))
	defer server.Close()
	path := bindingTestConfig(t, server.URL)
	t.Setenv("SSH_ORIGINAL_COMMAND", "mutagen-agent --ssh-key user-controlled synchronizer")
	if err := run([]string{"--config", path, "--device", "device-1", "--dry-run"}); err == nil {
		t.Fatal("missing key accepted")
	}
	if calls.Load() != 0 {
		t.Fatalf("authorization requests = %d", calls.Load())
	}
}
