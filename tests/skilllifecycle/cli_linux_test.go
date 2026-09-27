package skilllifecycle

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeCLILifecycle(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST") != "1" {
		t.Skip("requires host CLI, real Mutagen/SSH and isolated production daemons")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	f := readFixture(t)
	if !f.CLILifecycle || !f.SSHExport {
		t.Fatal("missing explicit CLI lifecycle fixture")
	}
	// The standalone production sync command resolves the installer's canonical user and group names.
	// Retain the same unprivileged UID/GID used by the shipped Worker unit fixture.
	command(t, ctx, "groupadd", "--non-unique", "--gid", "22000", "agent-remote")
	command(t, ctx, "useradd", "--non-unique", "--uid", "22000", "--gid", "22000", "--no-create-home", "--shell", "/usr/sbin/nologin", "agent-remote")
	command(t, ctx, "install", "--directory", "--mode=0700", "--owner=ar-proof-worker", "--group=ar-proof-worker", "/home/ar-proof-worker")
	prepareDaemons(t, ctx, f)
	policy, err := os.ReadFile("/proof/agent-remote-runtime.sudoers")
	if err != nil {
		t.Fatal(err)
	}
	policy = []byte(strings.NewReplacer("agent-remote ALL", "ar-proof-worker ALL", "/usr/local/bin/", "/proof/").Replace(string(policy)))
	if err := os.WriteFile("/etc/sudoers.d/agent-remote-proof", policy, 0440); err != nil {
		t.Fatal(err)
	}
	command(t, ctx, "visudo", "-cf", "/etc/sudoers.d/agent-remote-proof")
	startExportSSH(t, ctx, true)
	t.Log("CLI_LIFECYCLE_READY")
	await(t, ctx, "host CLI first publication", func() bool {
		checkPendingCLICleanup(t, ctx)
		_, err := os.Stat("/proof/control/restart")
		return err == nil
	})
	command(t, ctx, "systemctl", "stop", workerUnit, helperUnit)
	startDaemons(t, ctx)
	if err := os.WriteFile("/proof/control/restarted", nil, 0600); err != nil {
		t.Fatal(err)
	}
	await(t, ctx, "host CLI inheritance and deletion", func() bool {
		_, err := os.Stat("/proof/control/done")
		return err == nil
	})
	data, err := os.ReadFile("/proof/control/sessions.json")
	var sessions []string
	if err != nil || json.Unmarshal(data, &sessions) != nil || len(sessions) != 2 || sessions[0] == sessions[1] {
		t.Fatal("invalid original CLI session identities")
	}
	for _, id := range sessions {
		awaitReclaimed(t, ctx, f, id)
	}
}
