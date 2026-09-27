package runtimehelper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

func configImportFixture(t *testing.T) (Engine, Request) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires the root Linux helper")
	}
	root := t.TempDir()
	engine := NewEngine(EngineConfig{StateRoot: filepath.Join(root, "runtime"), SkillStateRoot: filepath.Join(root, "skills"), AccountRoot: filepath.Join(root, "accounts"), WorkspaceRoot: filepath.Join(root, "workspaces"), NodeUser: "nobody", NodeID: "33333333-3333-4333-8333-333333333333"})
	if err := os.Mkdir(engine.config.AccountRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := ConfigImportRequest{DirectoryMode: "legacy", Account: toolaccounts.ImportConfigPayload{
		UserID: "11111111-1111-4111-8111-111111111111", ToolAccountID: "22222222-2222-4222-8222-222222222222", ToolType: "claude", RuntimeBackend: "docker_sandbox",
		Files: []toolaccounts.ImportConfigFile{{Path: "~/.claude/settings.json", ContentBase64: "e30=", Mode: 0o600}},
	}}
	mapped, err := Map(payload)
	if err != nil {
		t.Fatal(err)
	}
	return engine, Request{Version: 1, Operation: "import_account_config", RequestID: "import_tool_account_config:" + payload.Account.ToolAccountID + ":44444444-4444-4444-8444-444444444444", Payload: mapped}
}

func TestHelperConfigImportOwnsFilesAndReplaysWithoutOverwriting(t *testing.T) {
	engine, request := configImportFixture(t)
	result, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(result["account_remote_path"].(string), ".claude", "settings.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := engine.dockerRuntimeIdentity()
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if int(stat.Uid) != identity.UID || int(stat.Gid) != identity.GID || info.Mode().Perm() != 0o600 {
		t.Fatalf("wrong runtime permissions: %#v", stat)
	}
	fixtureRoot := filepath.Dir(engine.config.AccountRoot)
	// Only these two ancestors were created by testing.TempDir; TMPDIR itself stays untouched.
	for _, parent := range []string{fixtureRoot, filepath.Dir(fixtureRoot)} {
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	reader := exec.Command("cat", path)
	reader.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(identity.UID), Gid: uint32(identity.GID)}}
	if content, err := reader.Output(); err != nil || string(content) != "{}" {
		t.Fatalf("runtime user cannot read imported configuration: %q %v", content, err)
	}
	if err := os.WriteFile(path, []byte("later learning"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := NewEngine(engine.config)
	request.Payload["directory_mode"] = "managed_v1"
	request.Payload["directory_epoch"] = 1
	if _, err := restarted.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "later learning" {
		t.Fatalf("retry overwrote configuration: %q %v", content, err)
	}
	var changed ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Account.Files[0].ContentBase64 = "bmV3"
	request.Payload, _ = Map(changed)
	if _, err := restarted.Execute(context.Background(), request); err == nil {
		t.Fatal("changed input reused helper receipt")
	}
	entries, err := os.ReadDir(engine.config.SkillStateRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(engine.config.SkillStateRoot, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "content_base64") || strings.Contains(string(data), "later learning") {
			t.Fatal("receipt retained config bytes")
		}
	}
}

func TestHelperFenceRejectsDelayedLegacySkillBatchAfterRestart(t *testing.T) {
	engine, request := configImportFixture(t)
	request.Payload["directory_mode"] = "migrating"
	request.Payload["directory_epoch"] = 1
	if _, err := engine.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload.DirectoryMode = "legacy"
	payload.DirectoryEpoch = 0
	payload.Account.Files = append(payload.Account.Files, toolaccounts.ImportConfigFile{Path: "~/.claude/skills/demo/SKILL.md", ContentBase64: "YQ==", Mode: 0o600})
	payload.Account.Files[0].ContentBase64 = "bmV3"
	request.RequestID = strings.Replace(request.RequestID, "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555", 1)
	request.Payload, _ = Map(payload)
	_, err := NewEngine(engine.config).Execute(context.Background(), request)
	if !errors.Is(err, toolaccounts.ErrSkillManagerOwnsPath) {
		t.Fatalf("stale grant reopened imports: %v", err)
	}
	path := filepath.Join(engine.config.AccountRoot, payload.Account.UserID, "tool-accounts", "claude", payload.Account.ToolAccountID, ".claude", "settings.json")
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "{}" {
		t.Fatalf("partial batch changed settings: %q %v", content, err)
	}
}

func TestHelperImportIncompleteReceiptFailsClosed(t *testing.T) {
	engine, request := configImportFixture(t)
	if _, err := engine.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(engine.config.SkillStateRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "import-") {
			continue
		}
		path := filepath.Join(engine.config.SkillStateRoot, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var receipt skillmanager.AccountImportReceipt
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		receipt.State = "started"
		data, _ = json.Marshal(receipt)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewEngine(engine.config).Execute(context.Background(), request); !errors.Is(err, errConfigImportPending) {
		t.Fatalf("incomplete import was replayed: %v", err)
	}
}

func TestHelperImportRejectsForgedPrivilegedFields(t *testing.T) {
	for _, kind := range []string{"root", "uid", "path", "task", "account", "backend"} {
		t.Run(kind, func(t *testing.T) {
			engine, request := configImportFixture(t)
			var payload ConfigImportRequest
			if err := decodeStrictPayload(request.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "root":
				request.Payload["account_root"] = "/tmp"
			case "uid":
				request.Payload["uid"] = 0
			case "path":
				payload.Account.AccountRemotePath = "/tmp/other"
				request.Payload, _ = Map(payload)
			case "task":
				request.RequestID = "unrelated"
			case "account":
				payload.Account.ToolAccountID = "../other"
				request.Payload, _ = Map(payload)
			case "backend":
				payload.Account.RuntimeBackend = "unknown"
				request.Payload, _ = Map(payload)
			}
			if _, err := engine.Execute(context.Background(), request); err == nil {
				t.Fatal("forged helper input accepted")
			}
			files, err := os.ReadDir(engine.config.AccountRoot)
			if err != nil || len(files) != 0 {
				t.Fatalf("invalid request wrote files: %v %v", files, err)
			}
		})
	}
}

func TestHelperConfigImportFullQuotaThroughSocket(t *testing.T) {
	engine, request := configImportFixture(t)
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("x", 1<<20)
	payload.Account.Files = nil
	for index := range 8 {
		payload.Account.Files = append(payload.Account.Files, toolaccounts.ImportConfigFile{
			Path: fmt.Sprintf("~/.claude/config-%d.json", index), ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)), Mode: 0o600,
		})
	}
	var err error
	request.Payload, err = Map(payload)
	if err != nil {
		t.Fatal(err)
	}
	// Unix socket paths have a small platform limit independent of Go's test directory names.
	socketRoot, err := os.MkdirTemp("/tmp", "import-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketRoot)
	socketPath := filepath.Join(socketRoot, "helper.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(socketPath, -1, os.Getuid(), engine)
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.SetDeadline(time.Now().Add(helperRoundTripTimeout))
			server.handle(context.Background(), connection)
		}
		done <- acceptErr
	}()
	defer func() {
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		case <-time.After(helperRoundTripTimeout):
			t.Error("config import handler did not stop")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), helperRoundTripTimeout)
	defer cancel()
	result, err := NewClient(socketPath).Call(ctx, request.RequestID, request.Operation, request.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if result["files_written_count"] != float64(8) {
		t.Fatalf("incomplete import result: %v", result["files_written_count"])
	}
	for index := range 8 {
		path := filepath.Join(result["account_remote_path"].(string), ".claude", fmt.Sprintf("config-%d.json", index))
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatalf("full-quota file %d did not survive the socket: size=%d, error=%v", index, len(data), err)
		}
	}
}

func TestHelperConfigImportFailedReceiptDoesNotReplayAfterRepair(t *testing.T) {
	engine, request := configImportFixture(t)
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	accountPath := filepath.Join(engine.config.AccountRoot, payload.Account.UserID, "tool-accounts", "claude", payload.Account.ToolAccountID)
	if err := os.MkdirAll(accountPath, 0o755); err != nil {
		t.Fatal(err)
	}
	obstacle := filepath.Join(accountPath, ".claude")
	if err := os.WriteFile(obstacle, []byte("obstacle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), request); !errors.Is(err, errConfigImportFailed) {
		t.Fatalf("filesystem failure did not retain failed outcome: %v", err)
	}
	if err := os.Remove(obstacle); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(engine.config).Execute(context.Background(), request); !errors.Is(err, errConfigImportFailed) {
		t.Fatalf("failed import was replayed after repair: %v", err)
	}
	if _, err := os.Lstat(obstacle); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replay wrote configuration: %v", err)
	}
}
