package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestManagedLaunchSystemdStartingExit(t *testing.T) {
	for _, killed := range []bool{false, true} {
		t.Run(fmt.Sprint(killed), func(t *testing.T) {
			engine, input, spec, launch := managedSystemdLaunchFixture(t)
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if _, err := client.StartManagedSession(ctx, "original_managed_task", input); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(spec.TmuxSessionName))
			if _, err := nativeTmuxCommand(ctx, engine.config, spec, "wait-for", "-S", fmt.Sprintf("agent-remote-client-%x", digest[:8])); err != nil {
				t.Fatal(err)
			}
			bundle, _, err := engine.retainedNativeSkillSession(spec.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			for {
				if data, err := bundle.ReadFile("work/executions"); err == nil && string(data) == "entered" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("tool did not execute", ctx.Err())
				case <-time.After(30 * time.Millisecond):
				}
			}
			// Model a Helper crash after systemd launch but before a durable readiness receipt.
			data, err := json.Marshal(launch)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if killed {
				if err := exec.CommandContext(ctx, "systemctl", "kill", "--kill-whom=all", "--signal=SIGKILL", spec.UnitName).Run(); err != nil {
					t.Fatal(err)
				}
			} else {
				file, err := bundle.OpenFile("work/finish", os.O_CREATE|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for {
				state, err := readManagedNativeUnit(ctx, engine.config.SystemctlPath, spec.UnitName)
				if err == nil && (state.SubState == "exited" || state.ActiveState == "failed") && confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+spec.UnitName) == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("tool writers did not exit", ctx.Err())
				case <-time.After(30 * time.Millisecond):
				}
			}
			if err := os.Remove(engine.specPath(spec.SessionID)); err != nil {
				t.Fatal(err)
			}
			result, err := client.ReconcileSkillSession(ctx, "recover-unconfirmed-exit", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || result.Record == nil || result.Record.Unclean != killed {
				t.Fatal("starting exit classification or capture failed", result, err)
			}
			store, err := engine.openExistingSkillStateRoot()
			if err != nil {
				t.Fatal(err)
			}
			saved, err := skillmanager.ReadSessionLaunch(store, launch)
			_ = store.Close()
			if err != nil || saved.State != "observed" || saved.InvocationID == "" {
				t.Fatal("starting exit invented readiness", err)
			}
			publication := "published"
			if killed {
				publication = "detached"
			}
			acknowledged, err := client.AcknowledgeSkillFinalization(ctx, "starting-exit-ack", helperFinalizationAck(*result.Record, publication))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CleanupFinalizedSkillSession(ctx, "starting-exit-cleanup", acknowledged); err != nil {
				t.Fatal(err)
			}
			replay, err := client.ReconcileSkillSession(ctx, "starting-exit-replay", input.Snapshot.NodeID, spec.SessionID)
			if err != nil || replay.Record == nil || replay.Record.TreeDigest != result.Record.TreeDigest || replay.Record.Unclean != killed {
				t.Fatal("cleanup lost original capture", err)
			}
			if data, err := bundle.ReadFile("work/executions"); err != nil || string(data) != "entered" {
				t.Fatal("recovery lost or duplicated output", err)
			}
		})
	}
}
