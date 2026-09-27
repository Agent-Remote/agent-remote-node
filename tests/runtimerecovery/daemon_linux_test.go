package runtimerecovery

import (
	"context"
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
)

const (
	workerUID    = 22000
	workerUnit   = "agent-remote-node.service"
	helperUnit   = "agent-remote-runtime.service"
	configPath   = "/etc/agent-remote-node/config.json"
	stateRoot    = "/var/lib/agent-remote-runtime"
	skillRoot    = "/var/lib/agent-remote/skill-state"
	accountRoot  = "/var/lib/agent-remote/users"
	workerRoot   = "/var/lib/agent-remote-node"
	helperSocket = "/run/agent-remote/runtime.sock"
	lossSocket   = "/run/agent-remote/reply-loss.sock"
)

type recoveryFixture struct {
	RepairSource   bool   `json:"repair_source"`
	VerifySource   bool   `json:"verify_source"`
	URL            string `json:"url"`
	Token          string `json:"token"`
	UserToken      string `json:"user_token"`
	NodeID         string `json:"node_id"`
	UserID         string `json:"user_id"`
	AccountID      string `json:"account_id"`
	OriginalTaskID string `json:"original_task_id"`
	RecoveryKey    string `json:"recovery_key"`
	MissingBackup  bool   `json:"missing_backup"`
}

func prepareDaemons(t *testing.T, ctx context.Context, fixture recoveryFixture) config.Config {
	t.Helper()
	for _, path := range []string{filepath.Dir(configPath), workerRoot, accountRoot, stateRoot + "/sessions", "/proof/bin"} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(workerRoot, workerUID, workerUID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ServerURL: fixture.URL, NodeToken: fixture.Token, NodeID: fixture.NodeID,
		HeartbeatIntervalSeconds: 2, PollIntervalSeconds: 1, LedgerPath: workerRoot + "/ledger.json",
		SSHAuthorizedKeysPath: workerRoot + "/authorized_keys", WorkspaceRoot: accountRoot, AccountRoot: accountRoot,
		SkillStateRoot: skillRoot, AllowedRuntimeBackends: []string{"native"}, RuntimeSocketPath: lossSocket,
		RuntimeBinaryPath: "/proof/agent-remote-runtime", ClaudeRuntimePath: "/usr/bin/true"}
	writeConfig(t, cfg)
	account := filepath.Join(accountRoot, fixture.UserID, "tool-accounts/claude", fixture.AccountID)
	for _, path := range []string{account + "/.claude/skills/manual/empty", account + "/other"} {
		if err := os.MkdirAll(path, 0750); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string][]byte{
		".claude/.credentials.json":      []byte(`{"fixture":true}`),
		".claude/skills/manual/SKILL.md": []byte("---\nname: manual\ndescription: disposable recovery proof\n---\nOriginal instructions\n"),
		".claude/skills/manual/memory":   []byte("retained learning\x00\xff"),
		".claude/skills/root-state":      []byte("retained root auxiliary bytes"),
		"other/executable":               []byte("#!/bin/sh\nexit 0\n"),
	} {
		if err := os.WriteFile(filepath.Join(account, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(account+"/other/executable", 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("memory", account+"/.claude/skills/manual/link"); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(account, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, workerUID, workerUID)
	}); err != nil {
		t.Fatal(err)
	}
	// Count privileged launches without replacing their real systemd invocation or commands.
	script := "#!/bin/sh\nif [ \"$1\" != --version ]; then printf x >> /proof/writer-launches; fi\nexec /usr/bin/systemd-run \"$@\"\n"
	if err := os.WriteFile("/proof/bin/systemd-run", []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if fixture.VerifySource {
		// Let the first real target ACL write complete, then fail so original rollback restores it.
		script := "#!/bin/sh\nif [ ! -e /proof/target-acl-failed ]; then\n  touch /proof/target-acl-failed\n  /usr/bin/setfacl \"$@\"\n  exit 1\nfi\nexec /usr/bin/setfacl \"$@\"\n"
		if err := os.WriteFile("/proof/bin/setfacl", []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.RepairSource {
		// Interrupt the Helper after an actual target ACL write, leaving its transient writer alive.
		script := "#!/bin/sh\n/usr/bin/setfacl \"$@\"\ntouch /proof/repair-writer-ready\nwhile [ ! -e /proof/repair-writer-release ]; do sleep .05; done\n"
		if err := os.WriteFile("/proof/bin/setfacl", []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{helperUnit, workerUnit} {
		original, err := os.ReadFile("/proof/" + name)
		if err != nil {
			t.Fatal(err)
		}
		unit := strings.NewReplacer("/usr/local/bin/", "/proof/", "User=agent-remote", "User=ar-proof-worker", "Group=agent-remote", "Group=ar-proof-worker", "--group agent-remote", "--group ar-proof-worker", "--user agent-remote", "--user ar-proof-worker", "/opt/agent-remote/runtimes/claude/current/bin/claude", "/usr/bin/true", "@AGENT_REMOTE_WIREGUARD_LISTEN_PORT@", "51820").Replace(string(original))
		unit += "\n[Service]\nStandardOutput=null\nStandardError=null\nTimeoutStopSec=20\nEnvironment=PATH=/proof/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n"
		if err := os.WriteFile("/etc/systemd/system/"+name, []byte(unit), 0644); err != nil {
			t.Fatal(err)
		}
	}
	command(t, ctx, "daemon-reload")
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		_ = exec.CommandContext(clean, "systemctl", "stop", workerUnit, helperUnit).Run()
	})
	return cfg
}

func writeConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal("encode disposable daemon config")
	}
	if err := os.WriteFile(configPath, data, 0640); err != nil {
		t.Fatal("write disposable daemon config")
	}
	if err := os.Chown(configPath, 0, workerUID); err != nil {
		t.Fatal(err)
	}
}

func command(t *testing.T, ctx context.Context, args ...string) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := exec.CommandContext(bounded, "systemctl", args...).Run(); err != nil {
		t.Fatal("disposable systemd command failed", args)
	}
}

func daemonPID(t *testing.T, ctx context.Context, unit string, uid int) int {
	t.Helper()
	data, err := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", unit).Output()
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || parseErr != nil || pid <= 1 {
		t.Fatal("daemon has no live process", unit)
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil || !strings.Contains(string(status), fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)) {
		t.Fatal("daemon privilege boundary mismatch", unit)
	}
	if uid == workerUID && (!strings.Contains(string(status), "CapEff:\t0000000000000000\n") || !strings.Contains(string(status), "NoNewPrivs:\t1\n")) {
		t.Fatal("Worker retained privileged execution authority")
	}
	return pid
}

func await(t *testing.T, ctx context.Context, label string, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for " + label)
		case <-ticker.C:
		}
	}
}
