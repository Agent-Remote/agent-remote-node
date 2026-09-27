package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestHelperFinalizationReclamationUnprivilegedPeer(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RECLAMATION_CHILD") == "1" {
		var input finalizationChildInput
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadDir(input.Store); !errors.Is(err, os.ErrPermission) {
			t.Fatal("peer can traverse privileged content", err)
		}
		client := NewClient(input.Socket)
		called := false
		record, err := client.ReclaimSkillFinalization(context.Background(), "nonroot-reclaim", input.Record, func(ctx context.Context, challenge string, ack skillmanager.FinalizationAcknowledgement) (skillmanager.ReclamationAuthorization, error) {
			called = true
			grant, _, err := helperReclamationGrant(ctx, ack)
			grant.RequestID = challenge
			return grant, err
		})
		if input.Denied {
			if err == nil || called {
				t.Fatal("unauthorized peer reached reclamation")
			}
			if _, err := client.ResumeSkillReclamation(context.Background(), "denied-resume", input.Record.Binding.NodeID, input.Record.Binding.SessionID); err == nil {
				t.Fatal("unauthorized peer resumed reclamation")
			}
			return
		}
		if err != nil || record != input.Record || !called {
			t.Fatal("authorized unprivileged peer could not reclaim", err)
		}
		if record, err := client.ResumeSkillReclamation(context.Background(), "nonroot-resume", input.Record.Binding.NodeID, input.Record.Binding.SessionID); err != nil || record != input.Record {
			t.Fatal("unprivileged peer could not recover completion", err)
		}
		return
	}
	engine, _, capture := helperReclamationFixture(t, false, "started")
	client, _ := serveCaptureTest(t, engine, 65534)
	for _, parent := range []string{filepath.Dir(engine.config.SkillStateRoot), filepath.Dir(filepath.Dir(engine.config.SkillStateRoot))} {
		if err := os.Chmod(parent, 0755); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []uint32{65533, 65534} {
		input, _ := json.Marshal(finalizationChildInput{Socket: client.socketPath, Store: engine.config.SkillStateRoot, Record: capture, Denied: uid != 65534})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestHelperFinalizationReclamationUnprivilegedPeer$", "-test.v")
		command.Env = append(os.Environ(), "AGENT_REMOTE_RECLAMATION_CHILD=1")
		command.Stdin = bytes.NewReader(input)
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("UID %d: %v\n%s", uid, err, output)
		}
	}
}
