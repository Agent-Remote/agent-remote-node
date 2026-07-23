package main

import (
	"errors"
	"os/exec"
	"testing"
)

func TestSessionFromOriginalCommand(t *testing.T) {
	sessionID, err := sessionFromOriginalCommand("agent-remote-attach --session session-123")
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "session-123" {
		t.Fatalf("unexpected session ID %q", sessionID)
	}
}

func TestSessionFromOriginalCommandRejectsOtherCommands(t *testing.T) {
	for _, command := range []string{"", "sh -c id", "agent-remote-attach --session ../etc/passwd", "agent-remote-attach --session ok extra"} {
		if _, err := sessionFromOriginalCommand(command); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
}

func TestBindingFromOriginalCommand(t *testing.T) {
	kind, id, err := attachTargetFromOriginalCommand("agent-remote-attach --binding account-123")
	if err != nil {
		t.Fatal(err)
	}
	if kind != "binding" || id != "account-123" {
		t.Fatalf("unexpected target %q %q", kind, id)
	}
}

func TestDefaultConfigPathUsesEnvironment(t *testing.T) {
	t.Setenv("AGENT_REMOTE_NODE_CONFIG", "/custom/config.json")
	if path := defaultConfigPath(); path != "/custom/config.json" {
		t.Fatalf("unexpected config path %q", path)
	}
}

func TestChildExitErrorCanPreserveMissingCommandStatus(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 127").Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("expected exec exit error, got %v", err)
	}
	if exitError.ExitCode() != 127 {
		t.Fatalf("unexpected exit code %d", exitError.ExitCode())
	}
}
