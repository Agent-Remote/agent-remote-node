package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

func TestBindingAttachProbeRequiresInstalledV1Gateway(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		output    string
		supported bool
	}{
		{"v1", `{"supported":true,"protocol_version":1}`, true},
		{"future", `{"supported":true,"protocol_version":2}`, false},
		{"false", `{"supported":false,"protocol_version":1}`, false},
		{"missing_version", `{"supported":true}`, false},
		{"missing_supported", `{"protocol_version":1}`, false},
		{"version_string", `{"supported":true,"protocol_version":"1"}`, false},
		{"version_bool", `{"supported":true,"protocol_version":true}`, false},
		{"version_float", `{"supported":true,"protocol_version":1.0}`, false},
		{"numeric_true", `{"supported":1,"protocol_version":1}`, false},
		{"unknown_field", `{"supported":true,"protocol_version":1,"extra":true}`, false},
		{"trailing_json", `{"supported":true,"protocol_version":1} {}`, false},
		{"empty", "", false},
		{"null", "null", false},
		{"invalid", "old gateway", false},
		{"oversized", strings.Repeat(" ", 257) + `{"supported":true,"protocol_version":1}`, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := bindingProbeScript(t, "test \"$#\" = 1 && test \"$1\" = --protocol-info || exit 19\nprintf '%s' '"+fixture.output+"'\nprintf 'synthetic-private-stderr' >&2\n")
			got := ProbeBindingAttach(context.Background(), path)
			want := api.BindingAttachCapability{}
			if fixture.supported {
				want = api.BindingAttachCapability{Supported: true, ProtocolVersion: 1}
			}
			if got != want {
				t.Fatalf("capability = %#v, want %#v", got, want)
			}
		})
	}
}

func TestBindingAttachProbeFailsClosedForUnavailableGateway(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if got := ProbeBindingAttach(context.Background(), path); got != (api.BindingAttachCapability{}) {
			t.Fatalf("unavailable gateway accepted: %#v", got)
		}
	}
	path := bindingProbeScript(t, "printf '%s' '{\"supported\":true,\"protocol_version\":1}'\nexit 1\n")
	if got := ProbeBindingAttach(context.Background(), path); got.Supported {
		t.Fatal("failed gateway accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if got := ProbeBindingAttach(context.Background(), path); got.Supported {
		t.Fatal("non-executable gateway accepted")
	}
}

func TestBindingAttachProbeBoundsTimeAndInheritedPipes(t *testing.T) {
	for _, script := range []string{
		"sleep 10\n",
		"sleep 10 &\nprintf '%s' '{\"supported\":true,\"protocol_version\":1}'\n",
		"while :; do printf 'oversized-output'; done\n",
	} {
		path := bindingProbeScript(t, script)
		start := time.Now()
		if got := probeBindingAttach(context.Background(), path, 50*time.Millisecond); got.Supported {
			t.Fatal("unbounded gateway accepted")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("probe exceeded its bounded budget: %s", elapsed)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ProbeBindingAttach(ctx, bindingProbeScript(t, "exit 0\n")); got.Supported {
		t.Fatal("cancelled probe accepted")
	}
}

func bindingProbeScript(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "configured-gateway")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
