package runtimehelper

import (
	"context"
	"io"
	"os/exec"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) runMigrationWriter(ctx context.Context, copy skillmanager.AccountCopyReceipt, phase, executable string, arguments ...string) (succeeded, drained bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if ctx.Err() != nil {
		return false, false
	}
	identity := skillmanager.MigrationWriterIdentity{Copy: copy, Phase: phase,
		CommandDigest: migrationDigest(append([]string{e.config.SystemdRunPath, e.config.SystemctlPath, e.config.CgroupRoot, executable}, arguments...))}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return false, false
	}
	defer store.Close()
	record, err := skillmanager.BeginMigrationWriter(store, identity)
	if err != nil {
		return false, false
	}
	unit, err := skillmanager.MigrationWriterUnit(identity)
	if err != nil || ctx.Err() != nil {
		return false, false
	}
	// Keep exited services until their invocation and whole-cgroup result have been saved.
	args := []string{"--quiet", "--service-type=exec", "--unit=" + unit,
		"--description=" + migrationWriterDescription(record), "--property=User=root",
		"--property=RemainAfterExit=yes", "--property=KillMode=control-group", "--property=RuntimeMaxSec=600",
		"--property=TimeoutStopSec=10", "--property=Restart=no", "--property=LimitCORE=0",
		"--property=StandardOutput=null", "--property=StandardError=null", "--", executable}
	args = append(args, arguments...)
	command := exec.CommandContext(ctx, e.config.SystemdRunPath, args...)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	// A failed/lost admission reply still requires inspection of the exact retained launch.
	_ = command.Run()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		proofCtx, release := context.WithTimeout(context.Background(), 10*time.Second)
		observed, err := e.observeMigrationWriter(proofCtx, store, record)
		release()
		if err != nil {
			return false, false
		}
		record = observed
		if record.State == "succeeded" || record.State == "failed" {
			e.cleanupMigrationWriter(record)
			return record.State == "succeeded", true
		}
		select {
		case <-ctx.Done():
			return false, false
		case <-ticker.C:
		}
	}
}

func migrationWriterDescription(record skillmanager.MigrationWriterReceipt) string {
	return "Agent Remote migration " + record.LaunchID
}
