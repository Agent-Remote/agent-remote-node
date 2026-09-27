package worker

import (
	"context"
	"errors"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

type snapshotPreparationClient interface {
	snapshotLeaseClient
	GetSkillSnapshot(context.Context, skillmanager.SkillSnapshotIdentity) (api.SkillSnapshot, error)
	ReadSkillSnapshotFile(context.Context, skillmanager.SkillSnapshotIdentity, skillmanager.Entry, io.Writer) error
}

type snapshotPreparationHelper interface {
	PrepareManagedSessionSpec(context.Context, string, runtimehelper.ManagedSessionSpecRequest) (map[string]any, error)
	PrepareSkillSnapshot(context.Context, string, skillmanager.SkillSnapshot, runtimehelper.SkillSnapshotDownloader) error
}

type managedSnapshotPreparation struct {
	Identity    skillmanager.SkillSnapshotIdentity
	InputDigest string
	RuntimeUID  uint32
}

// prepareManagedSnapshot composes authenticated downloads and Helper preparation under one lease.
// Its receipt is local preparation only. Dispatch must keep launch and ambiguous-start recovery separate.
func prepareManagedSnapshot(ctx context.Context, client snapshotPreparationClient, helper snapshotPreparationHelper, requestID string, binding skillmanager.SkillSnapshotIdentity, attempt int64, session toolsessions.CreatePayload) (managedSnapshotPreparation, error) {
	if err := validateSnapshotPreparationInput(requestID, binding, attempt, session); err != nil {
		return managedSnapshotPreparation{}, err
	}
	var prepared managedSnapshotPreparation
	err := withSnapshotLease(ctx, client, binding, attempt, func(workCtx context.Context) error {
		snapshot, err := client.GetSkillSnapshot(workCtx, binding)
		if err != nil {
			return err
		}
		if err := snapshot.Validate(binding); err != nil {
			return err
		}
		request, err := runtimehelper.NewManagedSessionSpecRequest(snapshot, session)
		if err != nil {
			return err
		}
		prepared, err = prepareManagedSnapshotInput(workCtx, client, helper, requestID, snapshot, request)
		return err
	})
	if err != nil {
		return managedSnapshotPreparation{}, err
	}
	return prepared, nil
}

func validateSnapshotPreparationInput(requestID string, binding skillmanager.SkillSnapshotIdentity, attempt int64, session toolsessions.CreatePayload) error {
	if binding.Validate() != nil || binding.RuntimeBackend != "native" || attempt <= 0 || attempt > 2147483647 || requestID == "" ||
		session.SessionID != binding.SessionID || session.UserID != binding.UserID || session.ToolAccountID != binding.AccountID || session.RuntimeBackend != binding.RuntimeBackend {
		return errors.New("invalid managed snapshot preparation input")
	}
	return nil
}

func prepareManagedSnapshotInput(ctx context.Context, client snapshotPreparationClient, helper snapshotPreparationHelper, requestID string, snapshot skillmanager.SkillSnapshot, request runtimehelper.ManagedSessionSpecRequest) (managedSnapshotPreparation, error) {
	if err := ctx.Err(); err != nil {
		return managedSnapshotPreparation{}, err
	}
	result, err := helper.PrepareManagedSessionSpec(ctx, requestID, request)
	if err != nil {
		return managedSnapshotPreparation{}, err
	}
	uid, err := takeRuntimeUID(result)
	if err != nil {
		return managedSnapshotPreparation{}, err
	}
	if err := ctx.Err(); err != nil {
		return managedSnapshotPreparation{}, err
	}
	if err := helper.PrepareSkillSnapshot(ctx, requestID, snapshot, func(ctx context.Context, entry skillmanager.Entry, target io.Writer) error {
		return client.ReadSkillSnapshotFile(ctx, snapshot.SkillSnapshotIdentity, entry, target)
	}); err != nil {
		return managedSnapshotPreparation{}, err
	}
	return managedSnapshotPreparation{Identity: snapshot.SkillSnapshotIdentity, InputDigest: request.SnapshotInputDigest, RuntimeUID: uid}, nil
}
