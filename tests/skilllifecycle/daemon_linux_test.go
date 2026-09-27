package skilllifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const (
	workerUID  = 22000
	runtimeUID = 12345
	workerUnit = "agent-remote-node.service"
	helperUnit = "agent-remote-runtime.service"
	configPath = "/etc/agent-remote-node/config.json"
	stateRoot  = "/var/lib/agent-remote-runtime"
	skillRoot  = "/var/lib/agent-remote/skill-state"
	usersRoot  = "/var/lib/agent-remote/users"
	workerRoot = "/var/lib/agent-remote-node"
	claudePath = "/opt/agent-remote/runtimes/claude/proof/bin/claude"
)

func command(t *testing.T, ctx context.Context, name string, args ...string) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if exec.CommandContext(bounded, name, args...).Run() != nil {
		t.Fatalf("lifecycle command failed: %s", name)
	}
}

func writePrivate(t *testing.T, path string, data []byte, uid, gid int) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal("create private lifecycle file")
	}
	if err := os.Chown(path, uid, gid); err != nil {
		t.Fatal("set private lifecycle owner")
	}
}

func prepareDaemons(t *testing.T, ctx context.Context, fixture lifecycleFixture) {
	t.Helper()
	for _, path := range []string{filepath.Dir(configPath), workerRoot, usersRoot, stateRoot + "/sessions"} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(workerRoot, workerUID, workerUID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ServerURL: fixture.URL, NodeToken: fixture.Token, NodeID: fixture.NodeID,
		HeartbeatIntervalSeconds: 2, PollIntervalSeconds: 1,
		LedgerPath: workerRoot + "/ledger.json", SSHAuthorizedKeysPath: workerRoot + "/authorized_keys",
		WorkspaceRoot: usersRoot, AccountRoot: usersRoot, SkillStateRoot: skillRoot,
		AllowedRuntimeBackends: []string{"native"}, RuntimeSocketPath: "/run/agent-remote/runtime.sock",
		RuntimeBinaryPath: "/proof/agent-remote-runtime", ClaudeRuntimePath: claudePath,
	}
	if fixture.SSHExport {
		cfg.AttachBinaryPath = "/proof/agent-remote-attach"
	}
	if fixture.ExportQuota {
		policy := skillmanager.DefaultStatePolicy()
		policy.CheckpointBytes, policy.DirectoryBytes = 1024, 1024
		cfg.SkillStatePolicy = &policy
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal("encode private daemon configuration")
	}
	writePrivate(t, configPath, data, 0, workerUID)
	if err := os.Chmod(configPath, 0o640); err != nil {
		t.Fatal(err)
	}
	prepareAccount(t, ctx, fixture)
	for _, name := range []string{helperUnit, workerUnit} {
		original, err := os.ReadFile("/proof/" + name)
		if err != nil {
			t.Fatal("read shipped systemd unit")
		}
		unit := strings.NewReplacer(
			"/usr/local/bin/", "/proof/",
			"User=agent-remote", "User=ar-proof-worker",
			"Group=agent-remote", "Group=ar-proof-worker",
			"--group agent-remote", "--group ar-proof-worker",
			"--user agent-remote", "--user ar-proof-worker",
			"/opt/agent-remote/runtimes/claude/current/bin/claude", claudePath,
			"@AGENT_REMOTE_WIREGUARD_LISTEN_PORT@", "51820",
		).Replace(string(original))
		// Daemon logs never become test output, even when a tool or server fails.
		unit += "\n[Service]\nStandardOutput=null\nStandardError=null\nTimeoutStopSec=20\n"
		if err := os.WriteFile("/etc/systemd/system/"+name, []byte(unit), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command(t, ctx, "systemctl", "daemon-reload")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "systemctl", "stop", workerUnit, helperUnit).Run()
	})
	startDaemons(t, ctx)
}

func prepareAccount(t *testing.T, ctx context.Context, f lifecycleFixture) {
	t.Helper()
	digest := sha256.Sum256([]byte(f.UserID))
	username := fmt.Sprintf("ar-u-%x", digest[:6])
	command(t, ctx, "groupadd", "--gid", strconv.Itoa(runtimeUID), username)
	command(t, ctx, "useradd", "--uid", strconv.Itoa(runtimeUID), "--gid", strconv.Itoa(runtimeUID), "--no-create-home", "--shell", "/bin/sh", username)
	account := filepath.Join(usersRoot, f.UserID, "tool-accounts/claude", f.AccountID)
	for _, path := range []string{account, filepath.Join(account, ".claude"), filepath.Join(account, ".claude/skills"), workspacePath(f)} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, runtimeUID, runtimeUID); err != nil {
			t.Fatal(err)
		}
	}
	credentials, err := os.ReadFile("/proof/private/credentials.json")
	if err != nil || len(credentials) == 0 || len(credentials) > 64<<10 || !json.Valid(credentials) {
		t.Fatal("invalid private Claude credentials fixture")
	}
	writePrivate(t, filepath.Join(account, ".claude/.credentials.json"), credentials, runtimeUID, runtimeUID)
	writePrivate(t, filepath.Join(account, ".claude.json"), []byte(`{"hasCompletedOnboarding":true}`), runtimeUID, runtimeUID)
}

func startDaemons(t *testing.T, ctx context.Context) {
	t.Helper()
	command(t, ctx, "systemctl", "start", helperUnit)
	await(t, ctx, "Helper socket", func() bool {
		info, err := os.Lstat("/run/agent-remote/runtime.sock")
		return err == nil && info.Mode()&os.ModeSocket != 0
	})
	probe, err := exec.CommandContext(ctx, "runuser", "-u", "ar-proof-worker", "--", "/proof/agent-remote-runtime", "probe").Output()
	var report struct {
		Available bool            `json:"available"`
		Native    map[string]bool `json:"native"`
	}
	if err != nil || json.Unmarshal(probe, &report) != nil {
		t.Fatal("nonroot peer could not probe the production Helper")
	}
	if !report.Available {
		t.Fatal("Native dependencies are not ready", report.Native)
	}
	command(t, ctx, "systemctl", "start", workerUnit)
	assertDaemonIdentity(t, ctx, workerUnit, workerUID)
	assertDaemonIdentity(t, ctx, helperUnit, 0)
}

func assertDaemonIdentity(t *testing.T, ctx context.Context, unit string, uid int) {
	t.Helper()
	// Type=simple reports started before the child has applied User=/Group= and execed.
	// Observe the real daemon with its final identity before asserting the privilege boundary.
	await(t, ctx, "daemon executable and UID: "+unit, func() bool {
		data, err := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", unit).Output()
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || parseErr != nil || pid <= 1 {
			return false
		}
		process := fmt.Sprintf("/proc/%d", pid)
		executable, err := os.Readlink(process + "/exe")
		if err != nil || executable != "/proof/"+strings.TrimSuffix(unit, ".service") {
			return false
		}
		status, err := os.ReadFile(process + "/status")
		return err == nil && strings.Contains(string(status), fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid))
	})
}

func workspacePath(f lifecycleFixture) string {
	return filepath.Join(usersRoot, f.UserID, "workspaces", f.WorkspaceID, "files")
}
