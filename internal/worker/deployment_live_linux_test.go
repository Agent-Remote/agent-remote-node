package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func liveDeploymentHelper(t *testing.T, input skillmanager.SkillDeployment) (string, string, func()) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned Linux Helper")
	}
	root := t.TempDir()
	state := filepath.Join(root, "skills")
	store, err := skillmanager.OpenStateStore(state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = skillmanager.CloseAccountImports(store, skillmanager.AccountFence{Version: 1, NodeID: input.NodeID, UserID: input.UserID, AccountID: input.AccountID, DirectoryEpoch: 1})
	_ = store.Close()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "helper.sock")
	engine := runtimehelper.NewEngine(runtimehelper.EngineConfig{NodeID: input.NodeID, SkillStateRoot: state, StateRoot: filepath.Join(root, "runtime"), AccountRoot: filepath.Join(root, "accounts"), WorkspaceRoot: filepath.Join(root, "workspaces")})
	server := runtimehelper.NewServer(socket, -1, os.Geteuid(), engine)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error("Helper shutdown failed", err)
			}
		})
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Helper socket did not become available")
		}
		time.Sleep(time.Millisecond * 10)
	}
	return socket, state, stop
}

// TestDeploymentWorkerLiveServer uses only an explicitly supplied disposable control-plane fixture.
func TestDeploymentWorkerLiveServer(t *testing.T) {
	path := os.Getenv("AGENT_REMOTE_TEST_DEPLOYMENT_RESULT_FIXTURE")
	if path == "" {
		t.Skip("requires an isolated authenticated Server fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read disposable fixture")
	}
	var fixture struct {
		URL              string                       `json:"url"`
		Token            string                       `json:"token"`
		Input            skillmanager.SkillDeployment `json:"input"`
		Task             api.TaskEnvelope             `json:"task"`
		DropConfirmation bool                         `json:"drop_confirmation"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal("invalid disposable fixture")
	}
	socket, state, stop := liveDeploymentHelper(t, fixture.Input)
	store, err := ledger.Open(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	worker := Worker{cfg: config.Config{NodeID: fixture.Input.NodeID, RuntimeSocketPath: socket, AllowedRuntimeBackends: []string{"native"}}, client: api.NewClient(fixture.URL, fixture.Token), ledger: store}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = worker.executeTask(ctx, fixture.Task)
	if fixture.DropConfirmation {
		if err == nil {
			t.Fatal("dropped confirmation reported success")
		}
		pending, readErr := (deploymentJournal{store}).load(fixture.Task.TaskID)
		if readErr != nil || pending == nil || pending.entry.Status != deploymentPreparedPending {
			t.Fatal("lost response lacks durable original proposal", readErr)
		}
	} else if err != nil {
		t.Fatal("live worker deployment failed", err)
	}
	// Successful recovery after shutting down the Helper proves that inspection cannot replay preparation.
	stop()
	if err := worker.recoverDeploymentConfirmations(ctx); err != nil {
		t.Fatal("read-only live confirmation recovery failed", err)
	}
	confirmed, err := (deploymentJournal{store}).load(fixture.Task.TaskID)
	if err != nil || confirmed == nil || confirmed.entry.Status != deploymentPreparedConfirmed {
		t.Fatal("Server acceptance did not become durable", err)
	}
	if err := worker.executeTask(ctx, fixture.Task); err != nil {
		t.Fatal("exact accepted replay failed", err)
	}
	private, err := skillmanager.OpenExistingStateStore(state)
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	receipt, err := skillmanager.ReadDeploymentPreparation(ctx, private, fixture.Input, skillmanager.DefaultCopyPolicy())
	if err != nil || receipt != confirmed.record.Result.Preparation {
		t.Fatal("Server result differs from retained complete Helper directory", err)
	}
	t.Log("verified live Server content, worker lease, root Helper directory, durable confirmation and read-only replay")
}
