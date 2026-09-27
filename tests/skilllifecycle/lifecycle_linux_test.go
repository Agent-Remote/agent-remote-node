package skilllifecycle

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestNativeClaudeLifecycle(t *testing.T) {
	ctx, f := lifecycleSetup(t)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	marker := "learned-" + hex.EncodeToString(nonce[:])
	firstRun := newClaudeRunID(t)
	first := createSession(t, ctx, f, learningArgs(firstRun, "Use the learning skill to remember this exact fact: "+marker+". Then exit."))
	runAttached(t, ctx, first, false)
	firstReceipt := awaitReclaimed(t, ctx, f, first)
	assertClaudeSkillDiscovery(t, f, firstRun)
	t.Log("real Claude learning finalized, published and locally reclaimed")
	command(t, ctx, "systemctl", "stop", workerUnit)
	command(t, ctx, "systemctl", "stop", helperUnit)
	startDaemons(t, ctx)
	secondRun := newClaudeRunID(t)
	second := createSession(t, ctx, f, learningArgs(secondRun, "Use the learning skill to recall the previously saved fact. Then exit."))
	// Before the second tool starts, only production snapshot materialization could restore learning.
	learned, err := os.ReadFile(filepath.Join(skillRoot, "session-"+second, "work/learning/memory.txt"))
	if err != nil || string(learned) != marker {
		t.Fatal("next session did not materialize the original learned bytes")
	}
	runAttached(t, ctx, second, false)
	secondReceipt := awaitReclaimed(t, ctx, f, second)
	assertClaudeSkillDiscovery(t, f, secondRun)
	witness, err := os.ReadFile(filepath.Join(workspacePath(f), "inherited.txt"))
	if err != nil || string(witness) != marker {
		t.Fatal("second real Claude did not read inherited learning")
	}
	if firstReceipt.Capture.Binding.SnapshotID == secondReceipt.Capture.Binding.SnapshotID {
		t.Fatal("next session reused an original snapshot identity")
	}
	t.Log("restarted Worker/Helper and second real Claude preserved learned bytes")
}

func TestNativeDaemonLifecycle(t *testing.T) {
	ctx, f := lifecycleSetup(t)
	id := createSession(t, ctx, f, []string{"--version"})
	runAttached(t, ctx, id, true)
	awaitReclaimed(t, ctx, f, id)
	t.Log("real Claude --version exited cleanly; production daemons finalized, published and reclaimed")
}

func lifecycleSetup(t *testing.T) (context.Context, lifecycleFixture) {
	t.Helper()
	return lifecycleSetupWithin(t, 10*time.Minute)
}

func lifecycleSetupWithin(t *testing.T, timeout time.Duration) (context.Context, lifecycleFixture) {
	t.Helper()
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_LIFECYCLE_TEST") != "1" {
		t.Skip("requires disposable Server, systemd, real Linux Claude and explicit private credentials")
	}
	if os.Geteuid() != 0 {
		t.Fatal("orchestrator requires root inside the disposable container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	f := readFixture(t)
	prepareDaemons(t, ctx, f)
	await(t, ctx, "ordinary takeover and deployment", func() bool {
		result, err := f.request(ctx, "GET", "/api/v1/skills/operations/"+f.OperationID, nil)
		return err == nil && result.Status == "ready"
	})
	t.Log("ordinary takeover/deployment completed by independent nonroot Worker")
	return ctx, f
}

func learningArgs(runID, prompt string) []string {
	return []string{"-p", "--model", "haiku", "--session-id", runID, "--max-turns", "6", "--dangerously-skip-permissions", "--tools", "Read,Write,Skill", prompt}
}

func createSession(t *testing.T, ctx context.Context, f lifecycleFixture, argv []string) string {
	t.Helper()
	response, err := f.request(ctx, "POST", "/api/v1/sessions", map[string]any{
		"tool_type": "claude", "tool_account_id": f.AccountID, "workspace_id": f.WorkspaceID,
		"project_key": "skill-lifecycle",
		"argv":        argv,
	})
	if err != nil || response.Data.ID == "" {
		t.Fatal("ordinary session admission failed", err)
	}
	id := response.Data.ID
	await(t, ctx, "ordinary managed startup", func() bool {
		state, err := f.request(ctx, "GET", "/api/v1/sessions/"+id, nil)
		if err == nil && (state.Data.Status == "failed" || state.Data.Status == "stopped") {
			t.Fatal("managed session terminated before attach")
		}
		return err == nil && state.Data.Status == "running"
	})
	return id
}

func runAttached(t *testing.T, ctx context.Context, session string, versionOnly bool) {
	t.Helper()
	runAttachedWithin(t, ctx, session, versionOnly, 3*time.Minute)
}

func runAttachedWithin(t *testing.T, ctx context.Context, session string, versionOnly bool, timeout time.Duration) {
	t.Helper()
	// This exercises the production local attach executable, not SSH authorization or the CLI.
	// Session IDs are server UUIDs; reject anything that could become a shell metacharacter.
	if len(session) != 36 || strings.Trim(session, "0123456789abcdef-") != "" {
		t.Fatal("invalid server session identity")
	}
	line := "/proof/agent-remote-runtime attach --session " + session + " --runtime-backend native --node-config " + configPath
	attachCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(attachCtx, "script", "--quiet", "--return", "--command", line, "/dev/null")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	var output boundedTranscript
	if versionOnly {
		// This separate smoke case has an empty credential file and only runs --version.
		cmd.Stdout, cmd.Stderr = &output, &output
	}
	// Keep the pty open until natural exit; no tool output or login state enters test logs.
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := cmd.Run(); err != nil {
		t.Fatal("production attach or real Claude session failed")
	}
	if versionOnly && !regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+ \(Claude Code\)`).Match(output.data) {
		t.Fatal("real Claude version was absent from the anonymous terminal")
	}
}

type boundedTranscript struct{ data []byte }

func (b *boundedTranscript) Write(data []byte) (int, error) {
	remaining := 8192 - len(b.data)
	if remaining > 0 {
		b.data = append(b.data, data[:min(remaining, len(data))]...)
	}
	return len(data), nil
}

func awaitReclaimed(t *testing.T, ctx context.Context, f lifecycleFixture, session string) skillmanager.FinalizationReclamation {
	t.Helper()
	bundle := filepath.Join(skillRoot, "session-"+session)
	var receipt skillmanager.FinalizationReclamation
	await(t, ctx, "fresh remote authorization and durable local reclamation", func() bool {
		data, err := os.ReadFile(filepath.Join(bundle, "finalization/reclaimed.json"))
		return err == nil && json.Unmarshal(data, &receipt) == nil
	})
	if receipt.Capture.Binding.SessionID != session || receipt.Capture.Binding.NodeID != f.NodeID || receipt.Capture.Unclean || receipt.Capture.State != "published" {
		t.Fatal("reclamation did not preserve original clean published capture")
	}
	for _, path := range []string{filepath.Join(bundle, "work"), filepath.Join(bundle, "finalization/objects"), filepath.Join(stateRoot, "sessions", session)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("reclaimed session still retains transient runtime or redundant content")
		}
	}
	state, err := f.request(ctx, "GET", "/api/v1/sessions/"+session, nil)
	if err != nil || state.Data.Status != "stopped" {
		t.Fatal("Server did not confirm original termination")
	}
	return receipt
}
