package main

import "testing"

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
