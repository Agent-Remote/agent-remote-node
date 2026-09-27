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

func TestManagedLaunchSystemdLostAdmissionRetainsWork(t *testing.T) {
	for _, phase := range []string{"started", "starting"} {
		for _, browser := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", phase, browser), func(t *testing.T) {
				engine, input, spec, launch := managedSystemdLaunchFixtureWithBrowser(t, browser)
				server := NewServer("", -1, os.Geteuid(), engine)
				client := skillPreparationTestPeer(t, server.handle)
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				if _, err := client.StartManagedSession(ctx, "original_managed_task", input); err != nil {
					log, _ := exec.Command("journalctl", "-u", spec.UnitName, "--no-pager", "-n", "20", "-o", "cat").CombinedOutput()
					t.Fatalf("managed browser launch failed: %v\n%s", err, log)
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
						t.Fatal("tool did not run", ctx.Err())
					case <-time.After(30 * time.Millisecond):
					}
				}
				if phase == "starting" {
					data, err := json.Marshal(launch)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"), data, 0o600); err != nil {
						t.Fatal(err)
					}
					observed, err := client.ReconcileSkillSession(ctx, "restart-before-ready-receipt", input.Snapshot.NodeID, spec.SessionID)
					if err != nil || observed.State != "running" || observed.Record != nil {
						t.Fatal("starting runtime did not recover observation", observed, err)
					}
					store, err := engine.openExistingSkillStateRoot()
					if err != nil {
						t.Fatal(err)
					}
					saved, err := skillmanager.ReadSessionLaunch(store, launch)
					_ = store.Close()
					if err != nil || saved.State != "observed" || saved.InvocationID == "" {
						t.Fatal("observation became readiness or lost invocation", err)
					}
				}
				observation, err := client.DrainUnadmittedSkillSession(ctx, "broker-restarted", input.Snapshot.NodeID, spec.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if !browser {
					if observation.State != "running" || observation.Record != nil {
						t.Fatal("browser-disabled session was drained", observation)
					}
					if phase == "starting" {
						if _, err := client.RecoverManagedSession(ctx, "original_managed_task", input); err != nil {
							t.Fatal("leased recovery could not finish observed invocation", err)
						}
						store, err := engine.openExistingSkillStateRoot()
						if err != nil {
							t.Fatal(err)
						}
						saved, err := skillmanager.ReadSessionLaunch(store, launch)
						_ = store.Close()
						if err != nil || saved.State != "started" {
							t.Fatal("recovery did not retain actual readiness", err)
						}
						if data, err := bundle.ReadFile("work/executions"); err != nil || string(data) != "entered" {
							t.Fatal("observed recovery executed tool again", err)
						}
					}
					return
				}
				if observation.State != "finalized" || observation.Record == nil || !observation.Record.Unclean {
					t.Fatal("interrupted tool was not retained as unclean", observation)
				}
				if err := confirmEmptyCgroup(engine.config.CgroupRoot, "/system.slice/"+spec.UnitName); err != nil {
					t.Fatal(err)
				}
				if data, err := bundle.ReadFile("work/executions"); err != nil || string(data) != "entered" {
					t.Fatal("lost admission lost or repeated tool output", err)
				}
				replay, err := client.DrainUnadmittedSkillSession(ctx, "lost-reply", input.Snapshot.NodeID, spec.SessionID)
				if err != nil || replay.Record == nil || *replay.Record != *observation.Record {
					t.Fatal("drain replay changed original capture", err)
				}
				acknowledged, err := client.AcknowledgeSkillFinalization(ctx, "drained-ack", helperFinalizationAck(*observation.Record, "detached"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := client.CleanupFinalizedSkillSession(ctx, "drained-cleanup", acknowledged); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(spec.SessionRoot); !os.IsNotExist(err) {
					t.Fatal("acknowledged transient runtime was not cleaned", err)
				}
			})
		}
	}
}
