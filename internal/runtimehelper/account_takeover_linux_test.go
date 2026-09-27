package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func accountTakeoverFixture(t *testing.T) (Engine, skillmanager.AccountTakeoverBinding, string) {
	t.Helper()
	engine, request := configImportFixture(t)
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	digest, err := skillmanager.AccountInventoryDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := skillmanager.AccountTakeoverBinding{
		Version: 1, NodeID: engine.config.NodeID, UserID: payload.Account.UserID, AccountID: payload.Account.ToolAccountID,
		TakeoverID: "55555555-5555-4555-8555-555555555555", TaskID: "66666666-6666-4666-8666-666666666666",
		RuntimeBackend: "native", DirectoryEpoch: 1, InventoryDigest: digest,
	}
	root := filepath.Dir(engine.config.AccountRoot)
	engine.config.CgroupRoot = filepath.Join(root, "cgroups")
	if err := os.Mkdir(engine.config.CgroupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	engine.config.SkillStatePolicy = skillmanager.StatePolicy{CheckpointBytes: 1 << 20, DirectoryBytes: 1 << 20, Entries: 100, MinimumFreeBytes: 1}
	state, units, calls := filepath.Join(root, "unit-state"), filepath.Join(root, "unit-list"), filepath.Join(root, "calls")
	if err := os.WriteFile(state, []byte("LoadState=not-found\nActiveState=inactive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(units, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAKEOVER_UNIT_STATE", state)
	t.Setenv("TAKEOVER_UNIT_LIST", units)
	t.Setenv("TAKEOVER_CALLS", calls)
	engine.config.SystemctlPath = writeTestCommand(t, "systemctl", `
printf '%s\n' "$1" >> "$TAKEOVER_CALLS"
case "$1" in
  list-units) cat "$TAKEOVER_UNIT_LIST" ;;
  show) cat "$TAKEOVER_UNIT_STATE" ;;
  *) exit 77 ;;
esac
`)
	return engine, binding, root
}

func TestNativeTakeoverCaptureRetainsFenceAndIgnoresLaterSource(t *testing.T) {
	engine, binding, _ := accountTakeoverFixture(t)
	path := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID, ".claude", "skills")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "learned"), []byte("original"), 0o400); err != nil {
		t.Fatal(err)
	}
	receipt, err := engine.captureNativeAccountTakeover(context.Background(), binding, nil)
	if err != nil || !receipt.SourceExists {
		t.Fatalf("capture failed: %#v %v", receipt, err)
	}
	if err := engine.requireLegacyAccountRuntime(binding.UserID, binding.AccountID); !errors.Is(err, errAccountMigrationPending) {
		t.Fatal("capture did not fence delayed writer", err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	engine.config.SystemctlPath = "/missing/systemctl"
	repeated, err := NewEngine(engine.config).captureNativeAccountTakeover(context.Background(), binding, nil)
	if err != nil || repeated != receipt {
		t.Fatalf("retry reread source or runtime: %#v %v", repeated, err)
	}
}

func TestNativeTakeoverWaitsForHistoricalWriterWithoutStopping(t *testing.T) {
	for _, kind := range []string{"session", "binding"} {
		t.Run(kind, func(t *testing.T) {
			engine, binding, root := accountTakeoverFixture(t)
			backend := "native"
			writers := []skillmanager.AccountWriter{{Kind: kind, NodeID: binding.NodeID, ResourceID: binding.TaskID, RuntimeBackend: &backend}}
			binding.InventoryDigest, _ = skillmanager.AccountInventoryDigest(writers)
			state := filepath.Join(root, "unit-state")
			if err := os.WriteFile(state, []byte(unitFields("active", "", "success", "0", "0")), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, writers); !errors.Is(err, errTakeoverWritersActive) {
				t.Fatal("live writer accepted", err)
			}
			if err := engine.requireLegacyAccountRuntime(binding.UserID, binding.AccountID); !errors.Is(err, errAccountMigrationPending) {
				t.Fatal("waiting state did not retain fence", err)
			}
			if err := os.WriteFile(state, []byte(unitFields("inactive", "", "success", "1", "0")), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, writers); err != nil {
				t.Fatal("natural exit not accepted", err)
			}
			calls, err := os.ReadFile(filepath.Join(root, "calls"))
			if err != nil || strings.Contains(string(calls), "stop") || strings.Contains(string(calls), "kill") {
				t.Fatal("takeover tried to stop legacy writer", err)
			}
		})
	}
}

func TestNativeTakeoverFindsUnlistedUnitsAndLeftoverCgroups(t *testing.T) {
	for _, evidence := range []string{"live_unit", "populated_group", "missing_events", "missing_cgroup_root", "malformed_units", "duplicate_units", "dangling_state_root"} {
		t.Run(evidence, func(t *testing.T) {
			engine, binding, root := accountTakeoverFixture(t)
			unit := "agent-remote-session-aaaaaaaaaaaa.service"
			switch evidence {
			case "live_unit":
				if err := os.WriteFile(filepath.Join(root, "unit-list"), []byte(`[{"unit":"`+unit+`"}]`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte(unitFields("active", "", "success", "0", "0")), 0o600); err != nil {
					t.Fatal(err)
				}
			case "populated_group", "missing_events":
				path := filepath.Join(engine.config.CgroupRoot, "system.slice", unit)
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if evidence == "populated_group" {
					if err := os.WriteFile(filepath.Join(path, "cgroup.events"), []byte("populated 1\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "missing_cgroup_root":
				if err := os.RemoveAll(engine.config.CgroupRoot); err != nil {
					t.Fatal(err)
				}
			case "duplicate_units":
				if err := os.WriteFile(filepath.Join(root, "unit-list"), []byte(`[{"unit":"ignored","unit":"`+unit+`"}]`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "dangling_state_root":
				if err := os.Symlink("missing", engine.config.StateRoot); err != nil {
					t.Fatal(err)
				}
			case "malformed_units":
				if err := os.WriteFile(filepath.Join(root, "unit-list"), []byte("null"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, nil); err == nil {
				t.Fatal("uncertain local writer accepted", evidence)
			}
		})
	}
}

func TestNativeTakeoverRejectsUnverifiedHistoryAndPendingImport(t *testing.T) {
	for _, kind := range []string{"unknown_backend", "docker", "migration", "foreign_node", "pending_import", "corrupt_spec", "symlink_spec"} {
		t.Run(kind, func(t *testing.T) {
			engine, binding, _ := accountTakeoverFixture(t)
			backend := "native"
			writers := []skillmanager.AccountWriter{{Kind: "session", NodeID: binding.NodeID, ResourceID: binding.TaskID, RuntimeBackend: &backend}}
			switch kind {
			case "unknown_backend":
				writers[0].RuntimeBackend = nil
			case "docker":
				backend = "docker_sandbox"
			case "migration":
				writers[0].Kind, writers[0].TaskID = "backend", &binding.TaskID
			case "foreign_node":
				writers[0].NodeID = binding.TakeoverID
			case "pending_import":
				store, err := engine.openSkillStateRoot()
				if err != nil {
					t.Fatal(err)
				}
				err = skillmanager.BeginAccountImport(store, skillmanager.AccountImportReceipt{
					Version: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID,
					TaskID: "import_tool_account_config:" + binding.AccountID + ":" + binding.TaskID, InputDigest: strings.Repeat("b", 64), State: "started",
				})
				_ = store.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "corrupt_spec", "symlink_spec":
				root := filepath.Join(engine.config.StateRoot, "sessions", binding.TaskID)
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "corrupt_spec" {
					if err := os.WriteFile(filepath.Join(root, "spec.json"), []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink("missing", filepath.Join(root, "spec.json")); err != nil {
					t.Fatal(err)
				}
			}
			binding.InventoryDigest, _ = skillmanager.AccountInventoryDigest(writers)
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, writers); !errors.Is(err, errTakeoverWritersUnknown) {
				t.Fatalf("unverified evidence accepted: %v", err)
			}
		})
	}
}

func TestNativeTakeoverInspectsLocallyRetainedBinding(t *testing.T) {
	engine, binding, root := accountTakeoverFixture(t)
	id := binding.TaskID
	sessionRoot := filepath.Join(engine.config.StateRoot, "sessions", id)
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := SessionSpec{
		Version: 1, Kind: "binding", SessionID: id, UserID: binding.UserID,
		Username:      "ar-u-" + shortDigest(binding.UserID, 12),
		AccountPath:   filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID),
		WorkspacePath: filepath.Join(engine.config.WorkspaceRoot, binding.UserID),
		SessionRoot:   sessionRoot, RuntimeRoot: filepath.Dir(filepath.Dir(engine.config.ClaudeRuntimePath)),
		RuntimeCommand: "/opt/agent-remote/runtime/bin/claude", TmuxSessionName: "binding", TmuxSocketPath: filepath.Join(sessionRoot, "tmux", "tmux.sock"),
		UnitName: "agent-remote-session-" + shortDigest(id, 12) + ".service", NetworkNamespace: "ar-" + shortDigest(id, 10),
		RuntimeConfig: sessionRuntimeConfigFromEngine(engine.config),
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine.specPath(id), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unit-state"), []byte(unitFields("active", "", "success", "0", "0")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, nil); !errors.Is(err, errTakeoverWritersActive) {
		t.Fatal("local binding absent from Server inventory was skipped", err)
	}
}

func TestNativeTakeoverRejectsAccountSourceAliases(t *testing.T) {
	for _, position := range []string{"account", "config", "skills", "configured_dangling"} {
		t.Run(position, func(t *testing.T) {
			engine, binding, root := accountTakeoverFixture(t)
			account := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID)
			target := filepath.Join(account, ".claude", "skills")
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			selected := map[string]string{"account": account, "config": filepath.Join(account, ".claude"), "skills": target, "configured_dangling": engine.config.AccountRoot}[position]
			moved := filepath.Join(root, "preserved")
			if err := os.Rename(selected, moved); err != nil {
				t.Fatal(err)
			}
			link := moved
			if position == "configured_dangling" {
				link = "missing"
			}
			if err := os.Symlink(link, selected); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.captureNativeAccountTakeover(context.Background(), binding, nil); err == nil {
				t.Fatal("unverified alias became capture authority")
			}
			if _, err := os.Stat(moved); err != nil {
				t.Fatal("capture removed original alias target", err)
			}
		})
	}
}

func TestNativeTakeoverStrictSpecReaderRejectsUnsafeOwnershipAndAncestors(t *testing.T) {
	engine, _, _ := accountTakeoverFixture(t)
	path := filepath.Join(engine.config.StateRoot, "sessions")
	if err := os.MkdirAll(filepath.Join(path, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(path, "real", "spec.json")
	if err := os.WriteFile(filename, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := engine.openTakeoverRuntimeRoot("sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Symlink("real", filepath.Join(path, "alias")); err != nil {
		t.Fatal(err)
	}
	var spec SessionSpec
	if err := readTakeoverSpec(root, "alias/spec.json", &spec); err == nil {
		t.Fatal("spec ancestor link followed")
	}
	if err := os.Chown(filename, 12345, 12345); err != nil {
		t.Fatal(err)
	}
	if err := readTakeoverSpec(root, "real/spec.json", &spec); err == nil {
		t.Fatal("runtime-owned spec became quiescence evidence")
	}
}
