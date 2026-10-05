package runtimehelper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDockerCapabilityArgsRejectsHostEscapes(t *testing.T) {
	workspace := t.TempDir()
	for _, args := range [][]string{
		{"run", "--privileged", "alpine"},
		{"run", "--network=host", "alpine"},
		{"run", "-v", "/var/run/docker.sock:/var/run/docker.sock", "alpine"},
		{"run", "-p", "8080:80", "alpine"},
		{"build", filepath.Join(workspace, "..")},
	} {
		if err := validateDockerCapabilityArgs(args, "session_1", workspace); err == nil {
			t.Fatalf("command %v was accepted", args)
		}
	}
}

func TestValidateDockerCapabilityArgsAllowsWorkspaceBuild(t *testing.T) {
	workspace := t.TempDir()
	if err := validateDockerCapabilityArgs([]string{"build", "./service"}, "session_1", workspace); err != nil {
		t.Fatalf("workspace build rejected: %v", err)
	}
}

func TestScopeDockerCapabilityArgsAddsSessionBoundary(t *testing.T) {
	run := scopeDockerCapabilityArgs([]string{"run", "alpine"}, "session_1")
	joined := strings.Join(run, " ")
	if !strings.Contains(joined, "com.agent-remote.session=session_1") || !hasDockerFlag(run, "--name") {
		t.Fatalf("run command was not scoped: %v", run)
	}
	ps := scopeDockerCapabilityArgs([]string{"ps"}, "session_1")
	if !strings.Contains(strings.Join(ps, " "), "label=com.agent-remote.session=session_1") {
		t.Fatalf("ps command was not scoped: %v", ps)
	}
}

func TestDockerCapabilityTargetSkipsOptions(t *testing.T) {
	if got := dockerCapabilityTarget([]string{"exec", "--env", "A=B", "--workdir", "/workspace", "container", "sh"}); got != "container" {
		t.Fatalf("target = %q", got)
	}
	if got := dockerCapabilityTarget([]string{"logs", "container"}); got != "container" {
		t.Fatalf("logs target = %q", got)
	}
}

func TestPrepareDockerCapabilityMakesWrapperParentTraversable(t *testing.T) {
	root := t.TempDir()
	broker, _, wrapper, err := prepareDockerCapability(root, "session_1", root, "/usr/bin/docker", 1001, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if broker != nil {
		t.Cleanup(broker.closeBroker)
	}
	parentInfo, err := os.Stat(filepath.Dir(wrapper))
	if err != nil {
		t.Fatal(err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o755 {
		t.Fatalf("wrapper parent mode = %o, want 755", got)
	}
	wrapperInfo, err := os.Stat(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if got := wrapperInfo.Mode().Perm(); got != 0o555 {
		t.Fatalf("wrapper mode = %o, want 555", got)
	}
}
