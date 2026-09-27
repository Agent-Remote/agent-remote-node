package runtimerecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

type entryIdentity struct {
	Changed  int64
	Inode    uint64
	Device   uint64
	Mode     uint32
	UID      uint32
	GID      uint32
	Size     int64
	Modified int64
	Digest   string
	Link     string
}

func inventory(t *testing.T, root string) map[string]entryIdentity {
	t.Helper()
	result := make(map[string]entryIdentity)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("missing filesystem identity")
		}
		value := entryIdentity{Changed: stat.Ctim.Sec*1_000_000_000 + stat.Ctim.Nsec, Inode: stat.Ino, Device: uint64(stat.Dev), Mode: stat.Mode, UID: stat.Uid, GID: stat.Gid, Size: info.Size(), Modified: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			value.Digest = hex.EncodeToString(sum[:])
		}
		if info.Mode()&os.ModeSymlink != 0 {
			value.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[name] = value
		return nil
	}); err != nil {
		t.Fatal("cannot inspect complete disposable account inventory", err)
	}
	return result
}

func recoveryStatus(ctx context.Context, f recoveryFixture) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL+"/api/v1/tool-accounts/"+f.AccountID+"/runtime-migration/recover/"+f.RecoveryKey, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Authorization", "Bearer "+f.UserToken)
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ""
	}
	var body struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&body) != nil {
		return ""
	}
	return body.Data.Status
}

func TestRuntimeRecoveryThroughProductionDaemons(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_RUNTIME_RECOVERY_TEST") != "1" {
		t.Skip("requires isolated HTTP/CLI and privileged systemd acceptance")
	}
	if os.Geteuid() != 0 {
		t.Fatal("orchestrator must run inside the disposable root container")
	}
	data, err := os.ReadFile("/proof/private/fixture.json")
	if err != nil {
		t.Fatal("read disposable fixture")
	}
	var fixture recoveryFixture
	if json.Unmarshal(data, &fixture) != nil {
		t.Fatal("decode disposable fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cfg := prepareDaemons(t, ctx, fixture)
	command(t, ctx, "start", helperUnit)
	awaitHelper(t, ctx)
	dropped, stopProxy := loseOriginalHelperReply(t, ctx, fixture.OriginalTaskID)
	command(t, ctx, "start", workerUnit)
	oldWorker, oldHelper := daemonPID(t, ctx, workerUnit, workerUID), daemonPID(t, ctx, helperUnit, 0)
	if fixture.RepairSource {
		await(t, ctx, "actual interrupted target ACL", func() bool { _, err := os.Stat("/proof/repair-writer-ready"); return err == nil })
		command(t, ctx, "kill", "--kill-who=main", "--signal=KILL", helperUnit)
	}
	await(t, ctx, "ordinary Worker failure ledger after successful Helper execution", func() bool {
		if !dropped.Load() && !fixture.VerifySource && !fixture.RepairSource {
			return false
		}
		saved, err := ledger.Open(cfg.LedgerPath)
		if err != nil {
			return false
		}
		entry, ok, err := saved.Get(fixture.OriginalTaskID)
		return err == nil && ok && entry.Status == "failed"
	})
	command(t, ctx, "stop", workerUnit, helperUnit)
	stopProxy()
	if fixture.RepairSource {
		if err := os.WriteFile("/proof/repair-writer-release", []byte("release"), 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(fixture.OriginalTaskID + ":target:0"))
		unit := "agent-remote-own-" + hex.EncodeToString(digest[:16]) + ".service"
		await(t, ctx, "original interrupted writer exit", func() bool {
			data, err := exec.CommandContext(ctx, "systemctl", "show", "--property=SubState", "--value", unit).Output()
			state := strings.TrimSpace(string(data))
			return err == nil && (state == "dead" || state == "failed" || state == "exited")
		})
	}
	// Ensure Server sees the original failure even if daemon shutdown interrupted its first report.
	// Production Worker ledger replay performs that report after the replacement process starts.
	sum := sha256.Sum256([]byte(fixture.OriginalTaskID))
	backup := filepath.Join(stateRoot, "migrations", hex.EncodeToString(sum[:])[:32])
	account := filepath.Join(accountRoot, fixture.UserID, "tool-accounts/claude", fixture.AccountID)
	accountBefore, backupBefore := inventory(t, account), inventory(t, backup)
	originalMetadata := inventory(t, skillRoot)
	launchesBefore, err := os.ReadFile("/proof/writer-launches")
	expectedLaunches := 4
	if fixture.VerifySource {
		expectedLaunches = 5
	}
	if fixture.RepairSource {
		expectedLaunches = 2
	}
	if err != nil || len(launchesBefore) != expectedLaunches {
		t.Fatal("original migration did not execute its exact copy and ownership phases")
	}
	if fixture.MissingBackup {
		held := backup + ".retained-for-test"
		if err := os.Rename(backup, held); err != nil {
			t.Fatal(err)
		}
		backup = held
		backupBefore = inventory(t, backup)
	}
	cfg.RuntimeSocketPath = helperSocket
	writeConfig(t, cfg)
	command(t, ctx, "start", helperUnit)
	awaitHelper(t, ctx)
	command(t, ctx, "start", workerUnit)
	if daemonPID(t, ctx, workerUnit, workerUID) == oldWorker || daemonPID(t, ctx, helperUnit, 0) == oldHelper {
		t.Fatal("daemon replacement did not occur")
	}
	t.Log("RECOVERY_DAEMONS_READY")
	want := "succeeded"
	if fixture.MissingBackup {
		want = "failed"
	}
	await(t, ctx, "exact Server recovery result", func() bool {
		status := recoveryStatus(ctx, fixture)
		if status == "failed" || status == "succeeded" {
			if status != want {
				t.Fatal("unexpected original recovery outcome", status)
			}
			return true
		}
		return false
	})
	command(t, ctx, "stop", workerUnit, helperUnit)
	launchesAfter, err := os.ReadFile("/proof/writer-launches")
	if err != nil || string(launchesAfter) != string(launchesBefore) {
		t.Fatal("recovery started a new privileged writer")
	}
	if !reflect.DeepEqual(backupBefore, inventory(t, backup)) {
		t.Fatal("recovery changed retained backup")
	}
	if fixture.RepairSource && !fixture.MissingBackup {
		repaired := inventory(t, account)
		if len(repaired) != len(backupBefore) {
			t.Fatal("repair changed account entries")
		}
		for path, expected := range backupBefore {
			actual, ok := repaired[path]
			expected.Changed, expected.Inode = actual.Changed, actual.Inode
			if !ok || actual != expected {
				t.Fatal("repair did not restore exact original source", path)
			}
		}
	} else if !reflect.DeepEqual(accountBefore, inventory(t, account)) {
		t.Fatal("verification changed original account")
	}
	afterMetadata := inventory(t, skillRoot)
	for path, original := range originalMetadata {
		if original.Mode&syscall.S_IFMT == syscall.S_IFREG && afterMetadata[path] != original {
			t.Fatal("repair rewrote original receipt", path)
		}
	}
	t.Log("restarted unprivileged Worker and root Helper verified recovery with retained backup and unchanged writer count")
}

func awaitHelper(t *testing.T, ctx context.Context) {
	t.Helper()
	await(t, ctx, "live production Helper socket", func() bool {
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err := runtimehelper.NewClient(helperSocket).Call(probe, "recovery-proof-probe", "probe", map[string]any{})
		return err == nil
	})
}
