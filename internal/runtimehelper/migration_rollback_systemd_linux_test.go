package runtimehelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func migrationACLFailureCommand(t *testing.T, kind string) string {
	t.Helper()
	scripts, err := os.MkdirTemp("/var/tmp", "migration-rollback-proof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scripts) })
	// The first call fails target ownership; later calls exercise each real rollback ACL phase.
	failAt := 0
	switch kind {
	case "failed-rollback-access":
		failAt = 2
	case "failed-rollback-default":
		failAt = 3
	case "failed-rollback-traverse":
		failAt = 4
	}
	command := filepath.Join(scripts, "setfacl")
	counter := filepath.Join(scripts, "calls")
	firstCall := "exit 1"
	if kind == "failed-target-acl-residual" {
		firstCall = "/usr/bin/setfacl \"$@\"; exit 1"
	}
	script := "#!/bin/sh\nset -eu\ncount=0\n" +
		"if [ -f '" + counter + "' ]; then count=$(cat '" + counter + "'); fi\n" +
		"count=$((count + 1))\nprintf '%s\\n' \"$count\" > '" + counter + "'\n" +
		"if [ \"$count\" -eq 1 ]; then " + firstCall + "; fi\n" +
		"if [ \"$count\" -eq " + strconv.Itoa(failAt) + " ]; then exit 1; fi\n" +
		"exec /usr/bin/setfacl \"$@\"\n"
	if err := os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return command
}

func verifyAttestedMigrationRecovery(t *testing.T, engine Engine, store *os.Root, task, userID, accountID, account, backup string) {
	t.Helper()
	expected := engine.accountMigrationReceipt(task, userID, accountID, "docker_sandbox", "native", account, backup)
	original, err := skillmanager.ReadAccountMigration(store, expected)
	if err != nil || original.Version != 2 || original.State != "succeeded" {
		t.Fatal("new migration lacks attested terminal evidence", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := engine.recoverCompletedAccountMigration(ctx, store, original, "docker_sandbox", "native", account, backup); err != nil {
		t.Fatal("attested original did not pass passive recovery", err)
	}
	file := filepath.Join(backup, ".claude", "skills", "memory")
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.recoverCompletedAccountMigration(ctx, store, original, "docker_sandbox", "native", account, backup); err == nil {
		t.Fatal("passive recovery ignored changed historical backup permissions")
	}
	if err := os.Chmod(file, 0640); err != nil {
		t.Fatal(err)
	}
	after, err := skillmanager.ReadAccountMigration(store, expected)
	if err != nil || after != original {
		t.Fatal("passive recovery changed original completion", err)
	}
}

func prepareMigrationSourcePermissions(t *testing.T, engine Engine, account string) {
	t.Helper()
	identity, err := engine.dockerRuntimeIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(account, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, identity.UID, identity.GID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.applyDataACL(account, identity.Username); err != nil {
		t.Fatal(err)
	}
	if err := engine.grantManagedTraverse(account, identity.Username); err != nil {
		t.Fatal(err)
	}
}

func assertMigrationSourceReadable(t *testing.T, engine Engine, learned string) {
	t.Helper()
	identity, err := engine.dockerRuntimeIdentity()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(learned)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if int(stat.Uid) != identity.UID || int(stat.Gid) != identity.GID {
		t.Fatal("rollback did not restore source ownership")
	}
	// Go's outer temporary directory is outside the managed traversal ACL boundary.
	if err := os.Chmod(filepath.Dir(filepath.Dir(engine.config.AccountRoot)), 0711); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/cat", learned)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uint32(identity.UID), Gid: uint32(identity.GID), Groups: []uint32{},
	}}
	data, err := command.Output()
	if err != nil || string(data) != "original learned content" {
		t.Fatal("source identity cannot read the retained learning after rollback", err)
	}
}
