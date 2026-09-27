package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func finalizationTransferFixture(t *testing.T) (Engine, skillmanager.FinalizationRecord) {
	t.Helper()
	return finalizationTransferFixtureForTask(t, "")
}

func finalizationTransferFixtureForTask(t *testing.T, taskID string) (Engine, skillmanager.FinalizationRecord) {
	t.Helper()
	engine, spec, binding := nativeSkillFixtureForTask(t, false, taskID)
	bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	file, err := bundle.OpenFile("work/retained", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write([]byte("original\x00\xff"))
	if closeErr := file.Close(); err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	if taskID == "" {
		if _, err := engine.stopSession(context.Background(), map[string]any{"session_id": spec.SessionID}); err != nil {
			t.Fatal(err)
		}
	} else {
		// No process is launched by this fixture. Seed immutable objects with complete original
		// task identity to test read-only export, not managed launch or writer-stop authority.
		if _, err := skillmanager.FinalizeWorkTree(context.Background(), bundle, binding, false); err != nil {
			t.Fatal(err)
		}
	}
	record, err := skillmanager.ReadFinalization(bundle, binding)
	if err != nil {
		t.Fatal(err)
	}
	return engine, record
}

func TestHelperFinalizationDescriptorSurvivesTransientAndWorkRemoval(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	work := filepath.Join(engine.config.SkillStateRoot, "session-"+record.Binding.SessionID, "work")
	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(engine.config.StateRoot); err != nil {
		t.Fatal(err)
	}
	got, manifest, err := client.ReadSkillFinalization(context.Background(), "read-finalization", record.Binding)
	if err != nil || got != record || len(manifest.Entries) != 1 {
		t.Fatal("manifest depended on transient state", err)
	}
	file, entry, err := client.OpenSkillFinalizationObject(context.Background(), "read-object", got, manifest.Entries[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("overwrite")); err == nil {
		t.Fatal("retained descriptor is writable")
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("descriptor can escape exec", err)
	}
	content, err := io.ReadAll(file)
	if err != nil || skillmanager.VerifyContent(entry, content) != nil || string(content) != "original\x00\xff" {
		t.Fatal("retained bytes changed", err)
	}
}

func TestHelperFinalizationRejectsChangedAuthorityAndReadOnlyUpgrade(t *testing.T) {
	engine, record := finalizationTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	_, manifest, err := client.ReadSkillFinalization(context.Background(), "original", record.Binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"node", "account", "session", "snapshot", "epoch", "generation", "tree", "unclean", "digest"} {
		t.Run(change, func(t *testing.T) {
			changed, digest := record, manifest.Entries[0].SHA256
			switch change {
			case "node":
				changed.Binding.NodeID = changed.Binding.UserID
			case "account":
				changed.Binding.AccountID = changed.Binding.UserID
			case "session":
				changed.Binding.SessionID = changed.Binding.UserID
			case "snapshot":
				changed.Binding.SnapshotID = changed.Binding.UserID
			case "epoch":
				changed.Binding.DirectoryEpoch++
			case "generation":
				changed.Binding.LibraryGeneration++
			case "tree":
				changed.TreeDigest = changed.Binding.InitialTreeDigest
			case "unclean":
				changed.Unclean = !changed.Unclean
			case "digest":
				digest = changed.TreeDigest
			}
			if file, _, err := client.OpenSkillFinalizationObject(context.Background(), "changed", changed, digest); err == nil || file != nil {
				if file != nil {
					_ = file.Close()
				}
				t.Fatal("changed finalization authority exported")
			}
		})
	}
	path := filepath.Join(engine.config.SkillStateRoot, "session-"+record.Binding.SessionID, "finalization/record.json")
	record.ObjectsVersion = 0
	data, _ := json.Marshal(record)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.ReadSkillFinalization(context.Background(), "legacy", record.Binding); err == nil {
		t.Fatal("transfer upgraded a legacy record")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("transfer changed legacy journal", err)
	}
}

type finalizationChildInput struct {
	Socket string
	Store  string
	Record skillmanager.FinalizationRecord
	Denied bool
}

func TestHelperFinalizationUnprivilegedReader(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_FINALIZATION_FD_CHILD") == "1" {
		var input finalizationChildInput
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if os.Geteuid() == 0 {
			t.Fatal("reader is privileged")
		}
		if file, err := os.Open(input.Store); err == nil {
			_ = file.Close()
			t.Fatal("worker traversed private store")
		}
		client := NewClient(input.Socket)
		inspected, inspectErr := client.InspectSkillFinalization(context.Background(), "inspect-frozen", input.Record.Binding.NodeID, input.Record.Binding.SessionID)
		if input.Denied {
			if inspectErr == nil {
				t.Fatal("unauthorized peer inspected frozen state")
			}
		} else if inspectErr != nil || inspected != input.Record {
			t.Fatal("unprivileged frozen inspection changed identity", inspectErr)
		}
		hold, held, holdErr := client.HoldSkillFinalization(context.Background(), "hold", input.Record.Binding)
		if input.Denied {
			if holdErr == nil || hold != nil {
				t.Fatal("unauthorized peer acquired a read hold")
			}
		} else {
			if holdErr != nil || held != input.Record || hold == nil {
				t.Fatal("unprivileged read hold changed identity", holdErr)
			}
			defer hold.Close()
			if _, err := hold.Write([]byte("changed")); err == nil {
				t.Fatal("worker wrote through read hold")
			}
		}
		record, manifest, err := client.ReadSkillFinalization(context.Background(), "read", input.Record.Binding)
		if input.Denied {
			if err == nil {
				t.Fatal("unauthorized peer admitted")
			}
			return
		}
		if err != nil || record != input.Record {
			t.Fatal("original receipt unavailable", err)
		}
		file, entry, err := client.OpenSkillFinalizationObject(context.Background(), "object", record, manifest.Entries[0].SHA256)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, err := file.Write([]byte("changed")); err == nil {
			t.Fatal("worker wrote retained bytes")
		}
		content, err := io.ReadAll(file)
		if err != nil || skillmanager.VerifyContent(entry, content) != nil {
			t.Fatal("descriptor bytes invalid", err)
		}
		verifyNonrootFrozenExport(t, client, input.Record)
		return
	}
	engine, record := finalizationTransferFixtureForTask(t, "66666666-6666-4666-8666-666666666666")
	client, _ := serveCaptureTest(t, engine, 65534)
	for _, parent := range []string{filepath.Dir(engine.config.SkillStateRoot), filepath.Dir(filepath.Dir(engine.config.SkillStateRoot))} {
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []uint32{65534, 65533} {
		input, _ := json.Marshal(finalizationChildInput{Socket: client.socketPath, Store: engine.config.SkillStateRoot, Record: record, Denied: uid != 65534})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestHelperFinalizationUnprivilegedReader$", "-test.v")
		command.Env = append(os.Environ(), "AGENT_REMOTE_FINALIZATION_FD_CHILD=1")
		command.Stdin = bytes.NewReader(input)
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("UID %d: %v\n%s", uid, err, output)
		}
	}
}
