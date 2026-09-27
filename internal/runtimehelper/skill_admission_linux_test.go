package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func enableManagedTestBrowser(t *testing.T, engine *Engine, input *ManagedSessionSpecRequest) {
	t.Helper()
	artifact := pinnedSkillRuntimeFixture(t)
	artifactRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(artifact.EgoBrowserWrapperPath))))
	for _, directory := range []string{artifactRoot, filepath.Dir(artifactRoot)} {
		if err := os.Chmod(directory, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	if err := filepath.WalkDir(artifact.EgoBrowserSkillPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if entry.IsDir() {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	}); err != nil {
		t.Fatal(err)
	}
	engine.config.EgoBrowserEnabled = true
	engine.config.EgoBrowserWrapperPath = artifact.EgoBrowserWrapperPath
	engine.config.EgoBrowserWrapperVersion = artifact.EgoBrowserWrapperVersion
	engine.config.EgoBrowserSkillPath = artifact.EgoBrowserSkillPath
	engine.config.EgoBrowserSkillVersion = artifact.EgoBrowserSkillVersion
	engine.config.EgoBrowserSkillTreeSHA256 = artifact.EgoBrowserSkillTreeSHA256
	brokerRoot := filepath.Join(filepath.Dir(engine.config.StateRoot), "broker")
	if err := os.MkdirAll(brokerRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	engine.config.EgoBrowserBrokerSocket = filepath.Join(brokerRoot, "peer.sock")
	listener, err := net.Listen("unix", engine.config.EgoBrowserBrokerSocket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	input.Session.EgoBrowserEnabled = true
	input.Session.EgoBrowserWrapperPath = engine.config.EgoBrowserWrapperPath
	input.Session.EgoBrowserWrapperVersion = engine.config.EgoBrowserWrapperVersion
	input.Session.EgoBrowserSkillPath = engine.config.EgoBrowserSkillPath
	input.Session.EgoBrowserSkillVersion = engine.config.EgoBrowserSkillVersion
	input.Session.EgoBrowserSkillTreeSHA256 = engine.config.EgoBrowserSkillTreeSHA256
	input.Session.EgoBrowserBrokerSocket = engine.config.EgoBrowserBrokerSocket
	input.Session.EgoBrowserProtocolVersion = engine.config.EgoBrowserProtocolVersion
	input.Session.EgoBrowserBrokerNonce = "process-only-fixture-nonce"
}

func TestHelperFinalizationAdmissionDrainsOnlyOriginalEnabledRuntime(t *testing.T) {
	for _, kind := range []string{"original", "disabled", "replacement", "replaced_during_grace", "populated", "missing_draft", "wrong_node"} {
		t.Run(kind, func(t *testing.T) {
			engine, input, spec, launch := preparedManagedLaunchWithConfig(t, func(engine *Engine, input *ManagedSessionSpecRequest) {
				if kind != "disabled" {
					enableManagedTestBrowser(t, engine, input)
				}
			})
			sealReconciliationLaunch(t, engine, launch)
			invocation := strings.Repeat("a", 32)
			if kind == "replacement" {
				invocation = strings.Repeat("b", 32)
			}
			command, actions := reconciliationSystemctl(t, spec, "running", invocation)
			engine.config.SystemctlPath = command
			if kind == "replaced_during_grace" {
				body, err := os.ReadFile(command)
				if err != nil {
					t.Fatal(err)
				}
				// The attempted graceful signal sees an independently replaced invocation.
				marker := filepath.Join(t.TempDir(), "replaced")
				script := strings.Replace(string(body), "case \"$1\" in", fmt.Sprintf("if test -f %q; then\ncat <<'STATE'\nLoadState=loaded\nActiveState=active\nSubState=running\nControlGroup=/system.slice/%s\nResult=success\nExecMainCode=0\nExecMainStatus=0\nInvocationID=%s\nTransient=yes\nUser=%s\nSTATE\nexit 0\nfi\ncase \"$1\" in\nkill) touch %q; exit 1;;", marker, spec.UnitName, strings.Repeat("b", 32), spec.Username, marker), 1)
				if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "populated" {
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
				if err := os.MkdirAll(group, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "missing_draft" {
				if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json")); err != nil {
					t.Fatal(err)
				}
			}
			nodeID := input.Snapshot.NodeID
			if kind == "wrong_node" {
				nodeID = input.Snapshot.UserID
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			observation, err := client.DrainUnadmittedSkillSession(context.Background(), "lost-admission", nodeID, spec.SessionID)
			if kind == "original" {
				if err != nil || observation.Record == nil || !observation.Record.Unclean {
					t.Fatal("original forced exit was not retained as unclean", observation, err)
				}
				replay, err := client.DrainUnadmittedSkillSession(context.Background(), "retry", nodeID, spec.SessionID)
				if err != nil || replay.Record == nil || *replay.Record != *observation.Record {
					t.Fatal("drain lost its immutable capture", err)
				}
				return
			}
			if err == nil {
				t.Fatal("uncertain runtime accepted without a verified running mount", observation)
			}
			if kind != "populated" {
				data, _ := os.ReadFile(actions)
				if strings.Contains(string(data), "stop") {
					t.Fatal("unrelated, disabled or replaced runtime stopped")
				}
			}
			if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID, "finalization")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("uncertain writers were frozen", err)
			}
		})
	}
}
