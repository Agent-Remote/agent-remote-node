package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// TestFirstUseWorkerLiveServer requires an isolated Server and an actual systemd/cgroup namespace.
func TestFirstUseWorkerLiveServer(t *testing.T) {
	path := os.Getenv("AGENT_REMOTE_TEST_SKILL_FIRST_USE_FIXTURE")
	if path == "" || os.Getenv("AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires disposable authenticated Server and root-owned isolated systemd")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read disposable fixture")
	}
	var fixture struct {
		URL                      string `json:"url"`
		Token                    string `json:"token"`
		NodeID                   string `json:"node_id"`
		OperationID              string `json:"operation_id"`
		DropTakeoverConfirmation bool   `json:"drop_takeover_confirmation"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal("invalid disposable fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := api.NewClient(fixture.URL, fixture.Token)
	first, err := client.PollTasks(ctx)
	if err != nil || len(first.Data.Tasks) != 1 {
		t.Fatal("ordinary first poll did not produce one takeover", err)
	}
	task := first.Data.Tasks[0]
	binding, err := decodeTakeoverTask(task, fixture.NodeID)
	if err != nil {
		t.Fatal("ordinary poll returned invalid takeover", err)
	}
	root := t.TempDir()
	helperConfig := runtimehelper.EngineConfig{
		NodeID: binding.NodeID, StateRoot: filepath.Join(root, "runtime"),
		SkillStateRoot: filepath.Join(root, "skills"), AccountRoot: filepath.Join(root, "accounts"),
		WorkspaceRoot: filepath.Join(root, "workspaces"), SystemctlPath: "systemctl", CgroupRoot: "/sys/fs/cgroup",
	}
	source := filepath.Join(helperConfig.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(source, "manual"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"manual/SKILL.md":   "---\nname: manual\ndescription: Existing instructions\n---\nOriginal manual skill\n",
		"manual/memory.txt": "before", "root-state": "auxiliary",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	release := firstUseLegacyWriter(t, binding.AccountID, source)
	socket, stop := serveFirstUseHelper(t, helperConfig)
	store, err := ledger.Open(filepath.Join(root, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	worker := Worker{
		cfg: config.Config{
			NodeID: fixture.NodeID, RuntimeSocketPath: socket, AllowedRuntimeBackends: []string{"native"},
		},
		client: client, ledger: store,
	}
	if err := worker.executeTask(ctx, task); !errors.Is(err, errTakeoverPending) {
		t.Fatal("live descendant did not block initial capture", err)
	}
	if actual, err := os.ReadFile(filepath.Join(source, "manual", "memory.txt")); err != nil || string(actual) != "before" {
		t.Fatal("takeover changed or stopped the old writer", err)
	}
	private, err := skillmanager.OpenExistingStateStore(helperConfig.SkillStateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	if bundle, _, _, err := skillmanager.OpenAccountCapture(private, binding); err == nil {
		_ = bundle.Close()
		t.Fatal("blocked capture published a bundle")
	}
	release()
	err = worker.executeTask(ctx, task)
	if fixture.DropTakeoverConfirmation {
		if !errors.Is(err, errTakeoverPending) {
			t.Fatal("lost publication response was not pending", err)
		}
	} else if err != nil {
		t.Fatal("first-use capture and upload failed", err)
	}
	bundle, capture, captured, err := skillmanager.OpenAccountCapture(private, binding)
	if err != nil {
		t.Fatal("original Helper capture was not durable", err)
	}
	_ = bundle.Close()
	assertFirstUseFile(t, captured, "manual/memory.txt", "late-learning")
	stop()
	if err := os.WriteFile(filepath.Join(source, "manual", "memory.txt"), []byte("later-unmanaged-change"), 0600); err != nil {
		t.Fatal(err)
	}
	// The committed original takeover can be acknowledged with the Helper offline.
	if err := worker.executeTask(ctx, task); err != nil {
		t.Fatal("committed takeover replay used Helper or lost source identity", err)
	}
	socket, _ = serveFirstUseHelper(t, helperConfig)
	worker.cfg.RuntimeSocketPath = socket
	next, err := client.PollTasks(ctx)
	if err != nil || len(next.Data.Tasks) != 1 {
		t.Fatal("ordinary next poll did not schedule deployment", err)
	}
	deploymentTask := next.Data.Tasks[0]
	identity, err := decodeDeploymentTask(deploymentTask, fixture.NodeID)
	if err != nil || identity.OperationID != fixture.OperationID || identity.AccountID != binding.AccountID {
		t.Fatal("deployment changed accepted identity", err)
	}
	input, err := client.GetSkillDeployment(ctx, identity, deploymentTask.LeaseAttempt)
	if err != nil {
		t.Fatal("resolved first-use input was unavailable", err)
	}
	assertFirstUseFile(t, input.Manifest, "manual/memory.txt", "late-learning")
	assertFirstUseFile(t, input.Manifest, "root-state", "auxiliary")
	origins := make(map[string]string)
	for _, entry := range input.Plan.Sources {
		origins[entry.Name] = entry.Origin
	}
	if len(origins) != 2 || origins["installed"] != "library" || origins["manual"] != "account_local" {
		t.Fatal("resolved deployment omitted original sources")
	}
	if err := worker.executeTask(ctx, deploymentTask); err != nil {
		t.Fatal("resolved first-use deployment failed", err)
	}
	confirmed, err := (deploymentJournal{store}).load(deploymentTask.TaskID)
	if err != nil || confirmed == nil || confirmed.entry.Status != deploymentPreparedConfirmed {
		t.Fatal("first-use result is not durable", err)
	}
	retained, err := skillmanager.ReadDeploymentPreparation(ctx, private, input, skillmanager.DefaultCopyPolicy())
	if err != nil || retained != confirmed.record.Result.Preparation {
		t.Fatal("confirmed input differs from retained complete directory", err)
	}
	bundle, replay, _, err := skillmanager.OpenAccountCapture(private, binding)
	if err != nil || replay != capture {
		t.Fatal("later deployment changed the initial capture", err)
	}
	_ = bundle.Close()
	t.Log("verified ordinary first-use polling, systemd descendant drain, original capture recovery and resolved deployment")
}

func assertFirstUseFile(t *testing.T, manifest skillmanager.Manifest, path, content string) {
	t.Helper()
	digest := sha256.Sum256([]byte(content))
	for _, entry := range manifest.Entries {
		if entry.Path == path && entry.Kind == "file" && entry.SHA256 == hex.EncodeToString(digest[:]) {
			return
		}
	}
	t.Fatal("original first-use content is absent or changed", path)
}
