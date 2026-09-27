package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func writerObservationFixture(t *testing.T) (Engine, *os.Root, skillmanager.MigrationWriterReceipt, string) {
	t.Helper()
	engine, binding, root := accountTakeoverFixture(t)
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := "migrate_tool_account_runtime:" + binding.AccountID + ":" + binding.TaskID
	copy := skillmanager.AccountCopyReceipt{Version: 1, WriterVersion: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, TaskID: task, InputDigest: strings.Repeat("a", 64), BootID: currentBootID(), Unit: skillmanager.AccountCopyUnit(task), State: "started"}
	if err := skillmanager.BeginAccountCopy(store, copy); err != nil {
		t.Fatal(err)
	}
	identity := skillmanager.MigrationWriterIdentity{Copy: copy, Phase: "copy", CommandDigest: strings.Repeat("c", 64)}
	record, err := skillmanager.BeginMigrationWriter(store, identity)
	if err != nil {
		t.Fatal(err)
	}
	return engine, store, record, root
}

func writerUnitFields(record skillmanager.MigrationWriterReceipt) string {
	return "LoadState=loaded\nActiveState=active\nSubState=exited\nControlGroup=/system.slice/" + record.Identity.Copy.Unit + "\nResult=success\nExecMainCode=1\nExecMainStatus=0\nInvocationID=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nTransient=yes\nUser=root\nRestart=no\nRemainAfterExit=yes\nDescription=" + migrationWriterDescription(record) + "\n"
}

func TestMigrationWriterObservationAfterReplacementNeverRelaunchesOrStopsLiveWork(t *testing.T) {
	engine, store, record, root := writerObservationFixture(t)
	live := strings.ReplaceAll(writerUnitFields(record), "SubState=exited", "SubState=running")
	statePath := filepath.Join(root, "unit-state")
	if err := os.WriteFile(statePath, []byte(live), 0600); err != nil {
		t.Fatal(err)
	}
	replacement := NewEngine(engine.config)
	observed, err := replacement.observeMigrationWriter(context.Background(), store, record)
	if err != nil || observed.State != "observed" || observed.InvocationID != strings.Repeat("b", 32) {
		t.Fatal("original live launch was not identified", err)
	}
	replacement.cleanupMigrationWriter(observed)
	calls, err := os.ReadFile(filepath.Join(root, "calls"))
	if err != nil || strings.Contains(string(calls), "stop") {
		t.Fatal("observation stopped a live writer", err)
	}
	if err := os.WriteFile(statePath, []byte(strings.ReplaceAll(writerUnitFields(record), strings.Repeat("b", 32), strings.Repeat("c", 32))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.observeMigrationWriter(context.Background(), store, observed); err == nil {
		t.Fatal("replacement invocation adopted")
	}
	if err := os.WriteFile(statePath, []byte(writerUnitFields(record)), 0600); err != nil {
		t.Fatal(err)
	}
	terminal, err := replacement.observeMigrationWriter(context.Background(), store, observed)
	if err != nil || terminal.State != "succeeded" {
		t.Fatal("original exited writer did not converge", err)
	}
	calls, _ = os.ReadFile(filepath.Join(root, "calls"))
	if strings.Contains(string(calls), "stop") {
		t.Fatal("passive recovery issued a stop")
	}
}

func TestMigrationWriterObservationRejectsForeignOrIncompleteRuntimeEvidence(t *testing.T) {
	for _, kind := range []string{"description", "invocation", "user", "transient", "restart", "retention", "cgroup", "populated", "boot", "missing", "duplicate", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			engine, store, record, root := writerObservationFixture(t)
			state := writerUnitFields(record)
			switch kind {
			case "description":
				state = strings.ReplaceAll(state, record.LaunchID, "foreign")
			case "invocation":
				state = strings.ReplaceAll(state, strings.Repeat("b", 32), strings.Repeat("0", 32))
			case "user":
				state = strings.ReplaceAll(state, "User=root", "User=12345")
			case "transient":
				state = strings.ReplaceAll(state, "Transient=yes", "Transient=no")
			case "restart":
				state = strings.ReplaceAll(state, "Restart=no", "Restart=always")
			case "retention":
				state = strings.ReplaceAll(state, "RemainAfterExit=yes", "RemainAfterExit=no")
			case "cgroup":
				state = strings.ReplaceAll(state, "/system.slice/", "/foreign/")
			case "populated":
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", record.Identity.Copy.Unit)
				if err := os.MkdirAll(group, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "boot":
				record.Identity.Copy.BootID = record.Identity.Copy.UserID
			case "missing":
				state = "LoadState=not-found\nActiveState=inactive\n"
			case "duplicate":
				state += "InvocationID=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"
			}
			if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte(state), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			if _, err := engine.observeMigrationWriter(ctx, store, record); err == nil {
				t.Fatal("unverified writer became terminal")
			}
			engine.cleanupMigrationWriter(record)
			calls, _ := os.ReadFile(filepath.Join(root, "calls"))
			if strings.Contains(string(calls), "stop") {
				t.Fatal("invalid evidence stopped a unit")
			}
		})
	}
}
