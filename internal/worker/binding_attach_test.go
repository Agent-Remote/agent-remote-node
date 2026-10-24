package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
)

func TestHeartbeatReprobesConfiguredBindingGateway(t *testing.T) {
	reports := make(chan api.BindingAttachCapability, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var heartbeat api.HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
			t.Error(err)
		}
		reports <- heartbeat.Runtime.RuntimeCapabilities.BindingAttach
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "installed-gateway")
	cfg := config.Config{
		NodeID: "node_1", ServerURL: server.URL, NodeToken: "synthetic-node-token",
		RuntimeSocketPath: filepath.Join(t.TempDir(), "missing.sock"), AttachBinaryPath: path,
	}.WithDefaults()
	w := New(cfg, api.NewClient(server.URL, cfg.NodeToken), nil)
	for _, supported := range []bool{true, false} {
		script := "#!/bin/sh\nexit 1\n"
		if supported {
			script = "#!/bin/sh\ntest \"$#\" = 1 && test \"$1\" = --protocol-info || exit 19\nprintf '%s' '{\"supported\":true,\"protocol_version\":1}'\n"
		}
		if err := os.WriteFile(path, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		if err := w.Heartbeat(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := <-reports
		if got.Supported != supported || supported && got.ProtocolVersion != 1 {
			t.Fatalf("installed gateway support %v reported as %#v", supported, got)
		}
	}
}
