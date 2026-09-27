package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type stoppedExportChildInput struct {
	Socket   string
	Store    string
	Binding  skillmanager.NodeExportBinding
	Denied   bool
	Recovery bool
}

func TestHelperFinalizationStoppedExportUnprivilegedGateway(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_STOPPED_EXPORT_CHILD") == "1" {
		var input stoppedExportChildInput
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			t.Fatal(err)
		}
		verifyStoppedExportChild(t, input)
		return
	}
	engine, _, binding := stoppedExportFixture(t)
	client, _ := serveCaptureTest(t, engine, 65534)
	for _, parent := range []string{filepath.Dir(engine.config.SkillStateRoot), filepath.Dir(filepath.Dir(engine.config.SkillStateRoot))} {
		mustExportFixture(t, os.Chmod(parent, 0o755))
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		uid      uint32
		recovery bool
	}{{65534, false}, {65533, false}, {65534, true}, {65533, true}} {
		uid := test.uid
		input, err := json.Marshal(stoppedExportChildInput{client.socketPath, engine.config.SkillStateRoot, binding, uid != 65534, test.recovery})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestHelperFinalizationStoppedExportUnprivilegedGateway$", "-test.v")
		command.Env = append(os.Environ(), "AGENT_REMOTE_STOPPED_EXPORT_CHILD=1")
		command.Stdin = bytes.NewReader(input)
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("UID %d: %v\n%s", uid, err, output)
		}
	}
}

func verifyStoppedExportChild(t *testing.T, input stoppedExportChildInput) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("export gateway remained privileged")
	}
	if file, err := os.Open(input.Store); err == nil {
		_ = file.Close()
		t.Fatal("gateway traversed private Helper state")
	}
	permission := skillmanager.NodeExportPermission{Binding: input.Binding,
		DeviceID: "77777777-7777-4777-8777-777777777777", SSHKeyID: "88888888-8888-4888-8888-888888888888",
		ExpiresAt: time.Now().Add(time.Minute).UTC(), RecheckSeconds: 10}
	grant := "fixture=.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	request := map[string]any{"version": 1, "grant": grant}
	if input.Recovery {
		request["recovery_version"] = 1
	}
	handshake, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	connection := &frozenExportConnection{input: bytes.NewReader(handshake)}
	authority := &frozenExportAuthority{permission: permission, grant: grant}
	identity := skillexport.Identity{NodeID: input.Binding.NodeID, SnapshotID: input.Binding.SnapshotID,
		DeviceID: permission.DeviceID, SSHKeyID: permission.SSHKeyID}
	err = skillexport.Serve(context.Background(), connection, identity, authority, NewClient(input.Socket))
	if input.Denied {
		if err == nil || connection.output.Len() != 0 {
			t.Fatal("unauthorized peer exported stopped work", err)
		}
		return
	}
	if err != nil || authority.checks < 3 {
		t.Fatal("authorized gateway lost recovery or reauthorization", err)
	}
	var verified bytes.Buffer
	if input.Recovery {
		err := skillexport.RelayRecovery(context.Background(), bytes.NewReader(connection.output.Bytes()), &verified, func(header skillexport.Header, _ bool) error {
			if header.Binding != input.Binding || !header.Unclean {
				return skillexport.ErrUnavailable
			}
			return nil
		})
		if err != nil || !bytes.Equal(verified.Bytes(), connection.output.Bytes()) {
			t.Fatal("unprivileged negotiated recovery did not complete", err)
		}
		return
	}
	err = skillexport.RelaySnapshot(context.Background(), bytes.NewReader(connection.output.Bytes()), &verified, func(header skillexport.Header, _ bool) error {
		if header.Binding != input.Binding || !header.Unclean || header.FileObjects != 1 || len(header.Manifest.Entries) != 1 ||
			skillmanager.VerifyContent(header.Manifest.Entries[0], []byte("original")) != nil {
			return skillexport.ErrUnavailable
		}
		return nil
	})
	if err != nil || !bytes.Equal(verified.Bytes(), connection.output.Bytes()) {
		t.Fatal("unprivileged recovery did not complete", err)
	}
}
