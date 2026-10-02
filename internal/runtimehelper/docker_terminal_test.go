package runtimehelper

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDockerTerminalSpecRejectsPartialAndRedirectedIdentity(t *testing.T) {
	engine := NewEngine(EngineConfig{StateRoot: t.TempDir()})
	base := DockerSessionSpec{Version: 1, Kind: "session", SessionID: "session_1", UserID: "user_1",
		TmuxSessionName: "managed", SandboxName: "sandbox", RuntimeUID: 1001, RuntimeGID: 1001,
		TerminalUID: 1002, TerminalGID: 1002,
		TmuxSocketPath: filepath.Join(engine.dockerSessionRoot("session_1"), "terminal", "tmux", "tmux.sock")}
	if err := engine.validateDockerSessionSpec(base, base.SessionID); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*DockerSessionSpec){
		func(s *DockerSessionSpec) { s.TerminalUID = 0 },
		func(s *DockerSessionSpec) { s.TerminalGID = -1 },
		func(s *DockerSessionSpec) { s.TmuxSocketPath = "/tmp/tmux-0/default" },
		func(s *DockerSessionSpec) { s.TmuxSocketPath = "" },
		func(s *DockerSessionSpec) { s.Version = 0 },
	} {
		bad := base
		mutate(&bad)
		if err := engine.validateDockerSessionSpec(bad, bad.SessionID); err == nil {
			t.Fatalf("accepted unsafe terminal: %+v", bad)
		}
	}
	legacy := base
	legacy.TmuxSocketPath, legacy.TerminalUID, legacy.TerminalGID = "", 0, 0
	if err := engine.validateDockerSessionSpec(legacy, legacy.SessionID); err != nil {
		t.Fatalf("legacy cleanup must remain available: %v", err)
	}
}

func TestDockerTmuxInspectionsDropPrivilegeAndPinSocket(t *testing.T) {
	spec := DockerSessionSpec{TmuxSocketPath: "/private/tmux.sock", TerminalUID: 1002, TerminalGID: 1003}
	cmd := dockerTmuxCommand("/usr/bin/tmux", spec, "has-session", "-t", "managed")
	if !reflect.DeepEqual(cmd.Args, []string{"/usr/bin/tmux", "-S", "/private/tmux.sock", "has-session", "-t", "managed"}) {
		t.Fatal(cmd.Args)
	}
	credential := cmd.SysProcAttr.Credential
	if credential.Uid != 1002 || credential.Gid != 1003 || !reflect.DeepEqual(credential.Groups, []uint32{1003}) {
		t.Fatalf("unsafe credential: %+v", credential)
	}
}

func TestDockerTerminalLaunchUsesPrivateStdinAndNoSecretArguments(t *testing.T) {
	root := t.TempDir()
	args := filepath.Join(root, "args")
	launcher := writeTestCommand(t, "systemd-run", `printf '%s\n' "$@" > "$TERMINAL_TEST_ARGS"
python3 -c 'import json,sys; value=json.load(sys.stdin); assert value["Environment"] == ["SYNTHETIC_SECRET=fixture-only"]; assert value["UID"] == 1002'
printf 'ready\n'`)
	t.Setenv("TERMINAL_TEST_ARGS", args)
	engine := NewEngine(EngineConfig{StateRoot: root, SystemdRunPath: launcher,
		RuntimeBinaryPath: "/fixed/runtime", TmuxBinaryPath: writeTestCommand(t, "tmux", "exit 1")})
	spec := DockerSessionSpec{SessionID: "session_1", TmuxSessionName: "managed", TmuxSocketPath: filepath.Join(root, "tmux.sock"), TerminalUID: 1002, TerminalGID: 1003}
	if err := engine.startDockerTerminal(spec, []string{"/fixed/docker", "sandbox", "exec"}, []string{"SYNTHETIC_SECRET=fixture-only"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "SYNTHETIC_SECRET") || strings.Contains(text, "fixture-only") || !strings.Contains(text, "--pipe") || !strings.Contains(text, "--property=NoNewPrivileges=yes") {
		t.Fatalf("unsafe launch arguments: %q", text)
	}
}

func TestDockerTerminalStopPreservesStateOnUnknownUnitStatus(t *testing.T) {
	spec := DockerSessionSpec{SessionID: "session_1", TmuxSocketPath: "/private/tmux.sock"}
	for _, test := range []struct {
		status  string
		success bool
	}{{"0", false}, {"1", false}, {"3", true}, {"4", true}} {
		engine := NewEngine(EngineConfig{SystemctlPath: writeTestCommand(t, "systemctl", "if [ \"$1\" = stop ]; then exit 1; fi\nexit "+test.status)})
		if err := engine.stopDockerTerminal(spec); (err == nil) != test.success {
			t.Fatalf("status %s: %v", test.status, err)
		}
	}
}
