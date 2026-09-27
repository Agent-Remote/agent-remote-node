package runtimehelper

import (
	"context"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) migrationWriterState(ctx context.Context, record skillmanager.MigrationWriterReceipt) (nativeUnitState, error) {
	unit, err := skillmanager.MigrationWriterUnit(record.Identity)
	if err != nil || currentBootID() != record.Identity.Copy.BootID {
		return nativeUnitState{}, errMigrationWritersUnknown
	}
	state, err := readUnitState(ctx, e.config.SystemctlPath, unit, true, true)
	if err != nil || state.LoadState != "loaded" || state.Description != migrationWriterDescription(record) ||
		state.Transient != "yes" || state.User != "root" || state.Restart != "no" || state.RemainAfterExit != "yes" ||
		!validManagedInvocationID(state.InvocationID) || record.InvocationID != "" && state.InvocationID != record.InvocationID ||
		state.ControlGroup != "" && state.ControlGroup != "/system.slice/"+unit {
		return nativeUnitState{}, errMigrationWritersUnknown
	}
	return state, nil
}

func migrationWriterExited(state nativeUnitState) bool {
	return state.ActiveState == "inactive" || state.ActiveState == "failed" || state.ActiveState == "active" && state.SubState == "exited"
}

func (e Engine) observeMigrationWriter(ctx context.Context, store *os.Root, expected skillmanager.MigrationWriterReceipt) (skillmanager.MigrationWriterReceipt, error) {
	saved, err := skillmanager.ReadMigrationWriter(store, expected.Identity)
	if err != nil || saved.LaunchID != expected.LaunchID || currentBootID() != saved.Identity.Copy.BootID {
		return saved, errMigrationWritersUnknown
	}
	if saved.State == "succeeded" || saved.State == "failed" {
		return saved, ctx.Err()
	}
	before, err := e.migrationWriterState(ctx, saved)
	if err != nil {
		return saved, err
	}
	saved, err = skillmanager.ObserveMigrationWriter(store, saved, before.InvocationID)
	if err != nil || !migrationWriterExited(before) {
		return saved, err
	}
	unit, err := skillmanager.MigrationWriterUnit(saved.Identity)
	if err != nil || confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+unit) != nil {
		return saved, errMigrationWritersUnknown
	}
	after, err := e.migrationWriterState(ctx, saved)
	if err != nil || before != after || confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+unit) != nil || ctx.Err() != nil {
		return saved, errMigrationWritersUnknown
	}
	outcome := "failed"
	if successfulNativeExit(after) {
		outcome = "succeeded"
	}
	return skillmanager.FinishMigrationWriter(store, saved, outcome)
}

func (e Engine) cleanupMigrationWriter(record skillmanager.MigrationWriterReceipt) {
	if record.State != "succeeded" && record.State != "failed" {
		return
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return
	}
	saved, err := skillmanager.ReadMigrationWriter(store, record.Identity)
	_ = store.Close()
	if err != nil || saved != record {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := e.migrationWriterState(ctx, record)
	unit, unitErr := skillmanager.MigrationWriterUnit(record.Identity)
	if err != nil || unitErr != nil || !migrationWriterExited(state) || confirmEmptyCgroup(e.config.CgroupRoot, "/system.slice/"+unit) != nil {
		return
	}
	// Terminal evidence is already durable. Cleanup cannot stop an active or replacement writer.
	command := exec.CommandContext(ctx, e.config.SystemctlPath, "stop", unit)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	_ = command.Run()
}
