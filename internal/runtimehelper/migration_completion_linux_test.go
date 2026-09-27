package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func completedMigrationFixture(t *testing.T, kind string) (Engine, Request, skillmanager.AccountMigrationReceipt, string, string) {
	t.Helper()
	engine, binding, root := accountTakeoverFixture(t)
	task := "migrate_tool_account_runtime:" + binding.AccountID + ":" + binding.TaskID
	account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
	backup := filepath.Join(engine.config.StateRoot, "migrations", shortDigest(task, 32))
	for _, directory := range []string{filepath.Join(account, ".claude"), filepath.Join(backup, ".claude")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(account, ".claude", ".credentials.json"), filepath.Join(backup, ".claude", ".credentials.json"), filepath.Join(account, "original"), filepath.Join(backup, "original")} {
		if err := os.WriteFile(file, []byte(`{"fixture":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	copy := skillmanager.AccountCopyReceipt{Version: 1, WriterVersion: 2, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, TaskID: task, InputDigest: migrationDigest([]string{"docker_sandbox", "native", account, backup}), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
	if kind == "previous-boot" || kind == "attested-rollback-previous-boot" || kind == "repair-previous-boot" {
		copy.BootID = binding.UserID
	}
	if kind == "pre-ownership-evidence" {
		copy.WriterVersion = 1
	}
	if kind == "legacy" {
		copy.WriterVersion = 0
	}
	receipt := skillmanager.AccountMigrationReceipt{Version: 1, Copy: copy, InputDigest: migrationDigest([]string{copy.InputDigest, engine.config.NodeUser, engine.config.SetfaclPath, engine.config.SystemdRunPath, engine.config.SystemctlPath, engine.config.CgroupRoot}), State: "started"}
	if strings.HasPrefix(kind, "attested-rollback") || strings.HasPrefix(kind, "repair") {
		receipt.Version = 2
	}
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := skillmanager.BeginAccountMigration(store, receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Version == 2 {
		seedMigrationSourceBaseline(t, engine, store, receipt, account)
	}
	if err := skillmanager.BeginAccountCopy(store, copy); err != nil {
		t.Fatal(err)
	}
	if kind != "legacy" {
		seedCompletedPhase(t, store, copy, "copy", "succeeded")
	}
	if kind != "copy-receipt-gap" {
		if err := skillmanager.FinishAccountCopy(store, copy, "copied"); err != nil {
			t.Fatal(err)
		}
	}
	if kind != "legacy" && kind != "copy-receipt-gap" {
		if copy.WriterVersion == 2 {
			if err := skillmanager.BeginMigrationOwnership(store, skillmanager.MigrationOwnershipIntent{Version: 1, Copy: copy, Phase: "target", Backend: "native"}); err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(kind, "repair") {
			_, err := skillmanager.BeginMigrationWriter(store, skillmanager.MigrationWriterIdentity{Copy: copy, Phase: "target-0", CommandDigest: strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
		}
		for index := 0; index < 3 && !strings.HasPrefix(kind, "repair"); index++ {
			if kind == "missing-target" && index == 2 {
				continue
			}
			seedCompletedPhase(t, store, copy, "target-"+strconv.Itoa(index), "succeeded")
		}
		if strings.HasPrefix(kind, "attested-rollback") || kind == "rollback" || kind == "incomplete-rollback" || kind == "failed-rollback" || kind == "rollback-intent-only" {
			if err := skillmanager.BeginMigrationOwnership(store, skillmanager.MigrationOwnershipIntent{Version: 1, Copy: copy, Phase: "rollback", Backend: "docker_sandbox"}); err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 3; index++ {
				if kind == "rollback-intent-only" {
					continue
				}
				if kind == "incomplete-rollback" && index == 2 {
					continue
				}
				outcome := "succeeded"
				if kind == "failed-rollback" && index == 2 {
					outcome = "failed"
				}
				seedCompletedPhase(t, store, copy, "rollback-"+strconv.Itoa(index), outcome)
			}
		}
	}
	if kind == "invalid-account" {
		if err := os.Remove(filepath.Join(account, ".claude", ".credentials.json")); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "missing-backup" {
		if err := os.RemoveAll(backup); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "foreign-unit" {
		if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte("LoadState=loaded\nActiveState=active\nSubState=running\nControlGroup=\nResult=success\nExecMainCode=0\nExecMainStatus=0\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "populated" {
		group := filepath.Join(engine.config.CgroupRoot, "system.slice", copy.Unit)
		if err := os.MkdirAll(group, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{Version: ProtocolVersion, RequestID: task, Operation: "migrate_account", Payload: map[string]any{"tool_account_id": binding.AccountID, "user_id": binding.UserID, "tool_type": "claude", "source_runtime_backend": "docker_sandbox", "target_runtime_backend": "native"}}
	return engine, request, receipt, root, backup
}

func seedCompletedPhase(t *testing.T, store *os.Root, copy skillmanager.AccountCopyReceipt, phase, outcome string) {
	t.Helper()
	identity := skillmanager.MigrationWriterIdentity{Copy: copy, Phase: phase, CommandDigest: strings.Repeat("a", 64)}
	writer, err := skillmanager.BeginMigrationWriter(store, identity)
	if err != nil {
		t.Fatal(err)
	}
	writer, err = skillmanager.ObserveMigrationWriter(store, writer, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := skillmanager.FinishMigrationWriter(store, writer, outcome); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationCompletedPhasesConvergeWithoutRepeatingAccountWrites(t *testing.T) {
	for _, kind := range []string{"target", "rollback", "copy-receipt-gap", "missing-target", "incomplete-rollback", "failed-rollback", "legacy", "pre-ownership-evidence", "rollback-intent-only", "previous-boot", "invalid-account", "missing-backup", "foreign-unit", "populated", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			engine, request, original, root, backup := completedMigrationFixture(t, kind)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			_, err := NewEngine(engine.config).Execute(ctx, request)
			want := "started"
			switch kind {
			case "target":
				want = "succeeded"
				if err != nil {
					t.Fatal("complete original target remained unresolved", err)
				}
			case "rollback":
				want = "failed"
				if !errors.Is(err, errMigrationFailed) {
					t.Fatal("complete original rollback lost failure outcome", err)
				}
			default:
				if !errors.Is(err, errMigrationWritersUnknown) {
					t.Fatal("incomplete recovery did not remain pending", err)
				}
			}
			store, openErr := engine.openSkillStateRoot()
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer store.Close()
			saved, readErr := skillmanager.ReadAccountMigration(store, original)
			if readErr != nil || saved.State != want {
				t.Fatal("wrong recovered state", readErr)
			}
			copy, readErr := skillmanager.ReadAccountCopy(store, original.Copy)
			if readErr != nil || copy.State != "copied" {
				t.Fatal("finished copy was not recovered", readErr)
			}
			calls, _ := os.ReadFile(filepath.Join(root, "calls"))
			for _, line := range strings.Split(string(calls), "\n") {
				if line != "" && line != "show" {
					t.Fatal("recovery executed a mutating command", line)
				}
			}
			if kind != "missing-backup" {
				data, readErr := os.ReadFile(filepath.Join(backup, "original"))
				if readErr != nil || string(data) != `{"fixture":true}` {
					t.Fatal("original backup changed", readErr)
				}
			}
		})
	}
}
