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
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

func managedSpecFixture(t *testing.T) (Engine, ManagedSessionSpecRequest, skillmanager.SkillSnapshot) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root Linux and real user/ACL tools")
	}
	if _, err := exec.LookPath("setfacl"); err != nil {
		t.Skip("requires setfacl")
	}
	root := t.TempDir()
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") == "1" {
		var err error
		root, err = os.MkdirTemp("/var/tmp", "ar-managed-launch-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
	}
	snapshot := preparationTestSnapshot(t, []byte("original"))
	engine := NewEngine(EngineConfig{
		StateRoot: filepath.Join(root, "runtime"), WorkspaceRoot: filepath.Join(root, "workspaces"), AccountRoot: filepath.Join(root, "accounts"),
		SkillStateRoot: filepath.Join(root, "skills"), NodeID: snapshot.NodeID, NodeUser: "root", SetfaclPath: "/usr/bin/setfacl",
		SystemctlPath: writeTestCommand(t, "systemctl", "printf 'LoadState=not-found\\nActiveState=inactive\\n'"),
	})
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, err = skillmanager.CloseAccountImports(store, skillmanager.AccountFence{Version: 1, NodeID: snapshot.NodeID, UserID: snapshot.UserID, AccountID: snapshot.AccountID, DirectoryEpoch: snapshot.DirectoryEpoch})
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	account := filepath.Join(engine.config.AccountRoot, snapshot.UserID, "tool-accounts", "claude", snapshot.AccountID, ".claude")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := snapshot.InputDigest()
	if err != nil {
		t.Fatal(err)
	}
	return engine, ManagedSessionSpecRequest{Snapshot: snapshot.SkillSnapshotIdentity, SnapshotInputDigest: digest, SystemReleases: testSkillSystemPins(), Session: toolsessions.CreatePayload{
		SessionID: snapshot.SessionID, ToolAccountID: snapshot.AccountID, ToolType: "claude", UserID: snapshot.UserID,
		WorkspaceID: "77777777-7777-4777-8777-777777777777", RuntimeBackend: "native", Timezone: "UTC", Locale: "C", TmuxSessionName: "managed-test", SandboxName: "unused-native", Argv: []string{"original"},
	}}, snapshot
}

func managedSpecRequest(t *testing.T, input ManagedSessionSpecRequest) Request {
	t.Helper()
	payload, err := Map(input)
	if err != nil {
		t.Fatal(err)
	}
	return Request{Version: ProtocolVersion, RequestID: "original_managed_task", Operation: managedSpecOperation, Payload: payload}
}

func TestManagedSpecLinuxCreateReplayAndBoundPreparation(t *testing.T) {
	engine, input, snapshot := managedSpecFixture(t)
	accountSkills := filepath.Join(engine.config.AccountRoot, input.Snapshot.UserID, "tool-accounts", "claude", input.Snapshot.AccountID, ".claude", "skills")
	if err := os.MkdirAll(accountSkills, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountSkills, "original"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	result, err := client.PrepareManagedSessionSpec(context.Background(), "original_managed_task", input)
	if err != nil || result["status"] != "spec_ready" {
		t.Fatal("spec creation failed", result, err)
	}
	spec, err := engine.loadSpec(input.Snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantAttachment := "--add-dir=/account/.agent-remote-attachments/" + spec.SessionID
	if len(spec.Argv) != 2 || spec.Argv[0] != "original" || spec.Argv[1] != wantAttachment {
		t.Fatalf("managed Native attachment grant changed user arguments: %q", spec.Argv)
	}
	if info, err := os.Stat(filepath.Join(spec.AccountPath, ".agent-remote-attachments", spec.SessionID)); err != nil || !info.IsDir() {
		t.Fatal("attachment directory missing before launch", err)
	}
	first, err := os.ReadFile(engine.specPath(spec.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PrepareManagedSessionSpec(context.Background(), "original_managed_task", input); err != nil {
		t.Fatal("exact replay failed", err)
	}
	second, _ := os.ReadFile(engine.specPath(spec.SessionID))
	if !bytes.Equal(first, second) {
		t.Fatal("replay rebuilt spec")
	}
	entries, err := os.ReadDir(accountSkills)
	if err != nil || len(entries) != 1 || entries[0].Name() != "original" {
		t.Fatal("shared skills changed", err)
	}
	for _, change := range []func(*ManagedSessionSpecRequest){
		func(i *ManagedSessionSpecRequest) { i.Session.Argv = []string{"changed"} },
		func(i *ManagedSessionSpecRequest) { i.SnapshotInputDigest = strings.Repeat("b", 64) },
		func(i *ManagedSessionSpecRequest) { i.Snapshot.TaskID = i.Snapshot.NodeID },
	} {
		other := input
		change(&other)
		if _, err := engine.Execute(context.Background(), managedSpecRequest(t, other)); err == nil {
			t.Fatal("changed input accepted")
		}
	}
	changedEngine := engine
	changedEngine.config.DNSResolvers = []string{"9.9.9.9"}
	if _, err := changedEngine.Execute(context.Background(), managedSpecRequest(t, input)); err == nil {
		t.Fatal("changed config accepted")
	}
	changedSnapshot := snapshot
	changedSnapshot.LibraryGeneration++
	opener := func(context.Context, string) (io.ReadCloser, error) {
		t.Error("unexpected object read")
		return nil, errors.New("unexpected read")
	}
	if err := engine.prepareTransferredSkillSnapshot(context.Background(), changedSnapshot, opener); err == nil {
		t.Fatal("changed snapshot accepted")
	}
	if err := engine.prepareTransferredSkillSnapshot(context.Background(), snapshot, func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("original")), nil
	}); err != nil {
		t.Fatal("matching snapshot rejected", err)
	}
	if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err == nil {
		t.Fatal("ready missing spec recreated")
	}
}

func TestManagedSpecLinuxDraftRecovery(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft_only", true: "published"}[published], func(t *testing.T) {
			engine, input, _ := managedSpecFixture(t)
			request := managedSpecRequest(t, input)
			digest, err := managedSpecInputDigest(request.RequestID, input, engine.config)
			if err != nil {
				t.Fatal(err)
			}
			intent := skillmanager.SessionSpecIntent{Version: 1, Identity: input.Snapshot, InputDigest: digest, State: "started"}
			store, err := engine.openExistingSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := skillmanager.BeginSessionSpecIntent(store, intent); err != nil {
				t.Fatal(err)
			}
			payload, _ := Map(input.Session)
			spec, err := engine.createManagedSpec(context.Background(), input, payload, digest)
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.requireManagedSpecReceipt(spec); err == nil {
				t.Fatal("unfinished spec allowed")
			}
			if err := engine.requireNativeAccountRuntime(spec); err == nil {
				t.Fatal("unfinished spec launch allowed")
			}
			before, err := os.ReadFile(engine.specPath(spec.SessionID))
			if err != nil {
				t.Fatal(err)
			}
			if !published {
				if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := engine.Execute(context.Background(), request); err != nil {
				t.Fatal("draft recovery failed", err)
			}
			after, _ := os.ReadFile(engine.specPath(spec.SessionID))
			if !bytes.Equal(before, after) {
				t.Fatal("recovery rebuilt original draft")
			}
			if err := engine.requireManagedSpecReceipt(spec); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedSpecLinuxRejectsForeignState(t *testing.T) {
	for _, kind := range []string{"directory", "bundle", "unit", "fence"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, _ := managedSpecFixture(t)
			var err error
			switch kind {
			case "directory":
				err = os.MkdirAll(filepath.Dir(engine.specPath(input.Snapshot.SessionID)), 0o700)
			case "bundle":
				err = os.Mkdir(filepath.Join(engine.config.SkillStateRoot, "session-"+input.Snapshot.SessionID), 0o700)
			case "unit":
				engine.config.SystemctlPath = writeTestCommand(t, "active-systemctl", "printf 'LoadState=loaded\\nActiveState=active\\n'")
			case "fence":
				err = os.Remove(filepath.Join(engine.config.SkillStateRoot, "account-"+input.Snapshot.AccountID+".json"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err == nil {
				t.Fatal("adopted foreign state")
			}
			if _, err := os.Stat(filepath.Join(engine.config.SkillStateRoot, "spec-intent-"+input.Snapshot.SessionID+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("wrote intent before rejecting foreign state", err)
			}
		})
	}
}

func TestManagedSpecLinuxImmutableFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root ownership")
	}
	for _, kind := range []string{"changed", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			directory, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()
			if err := publishManagedSpecFile(directory, "spec.json", []byte("original")); err != nil {
				t.Fatal(err)
			}
			if err := publishManagedSpecFile(directory, "spec.json", []byte("original")); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "changed":
				err = os.WriteFile(filepath.Join(path, "spec.json"), []byte("modified"), 0o644)
			case "symlink":
				if err = os.Rename(filepath.Join(path, "spec.json"), filepath.Join(path, "other")); err == nil {
					err = os.Symlink("other", filepath.Join(path, "spec.json"))
				}
			case "hardlink":
				err = os.Link(filepath.Join(path, "spec.json"), filepath.Join(path, "other"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := publishManagedSpecFile(directory, "spec.json", []byte("original")); err == nil {
				t.Fatal("repaired immutable file")
			}
		})
	}
}

func TestManagedSpecLinuxDraftExcludesNonce(t *testing.T) {
	engine, input, _ := managedSpecFixture(t)
	if _, err := engine.Execute(context.Background(), managedSpecRequest(t, input)); err != nil {
		t.Fatal(err)
	}
	spec, err := engine.loadSpec(input.Snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	spec.EgoBrowserBrokerNonce = "process-only-test-nonce"
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(spec.EgoBrowserBrokerNonce)) {
		t.Fatal("nonce serialized")
	}
	if err := engine.requireManagedSpecReceipt(spec); err != nil {
		t.Fatal("nonce changed immutable receipt", err)
	}
}

func TestManagedSpecLinuxReadyReplayPreservesDamagedState(t *testing.T) {
	for _, kind := range []string{"timezone", "resolv.conf", "identity", "boot", "draft"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, _ := managedSpecFixture(t)
			request := managedSpecRequest(t, input)
			if _, err := engine.Execute(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			spec, err := engine.loadSpec(input.Snapshot.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "timezone", "resolv.conf":
				err = os.WriteFile(filepath.Join(spec.SessionRoot, kind), []byte("changed"), 0o644)
			case "identity":
				spec.RuntimeUID++
			case "boot":
				spec.BootID = "old-boot"
			case "draft":
				err = os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.requireManagedSpecReceipt(spec); err == nil {
				t.Fatal("invalid ready spec accepted")
			}
			if kind == "timezone" || kind == "resolv.conf" || kind == "draft" {
				if _, err := engine.Execute(context.Background(), request); err == nil {
					t.Fatal("damaged ready spec repaired")
				}
			}
		})
	}
}

func TestManagedSpecLinuxAccountLinkAndEarlyCancellation(t *testing.T) {
	engine, input, _ := managedSpecFixture(t)
	request := managedSpecRequest(t, input)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Execute(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	account := filepath.Join(engine.config.AccountRoot, input.Snapshot.UserID, "tool-accounts", "claude", input.Snapshot.AccountID)
	if err := os.Rename(filepath.Join(account, ".claude"), filepath.Join(account, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original", filepath.Join(account, ".claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), request); err == nil {
		t.Fatal("followed account link")
	}
	if _, err := os.Stat(engine.specPath(input.Snapshot.SessionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created spec after rejection", err)
	}
}
