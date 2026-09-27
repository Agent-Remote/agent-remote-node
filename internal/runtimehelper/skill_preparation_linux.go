package runtimehelper

import (
	"context"
	"errors"
	"io"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) prepareTransferredSkillSnapshot(ctx context.Context, snapshot skillmanager.SkillSnapshot, open func(context.Context, string) (io.ReadCloser, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot.NodeID != e.config.NodeID || snapshot.RuntimeBackend != "native" {
		return errors.New("skill preparation requires the original Native node")
	}
	spec, err := e.loadSpec(snapshot.SessionID)
	if err != nil {
		return err
	}
	if spec.SkillSnapshotID != snapshot.SnapshotID {
		return errors.New("skill preparation requires a matching trusted session spec")
	}
	pins, err := parseSkillSystemPins(snapshot.SystemReleases)
	if err != nil {
		return err
	}
	digest, err := snapshot.InputDigest()
	if err != nil {
		return err
	}
	if spec.ManagedSkills != (ManagedSessionSpecBinding{}) {
		if err := e.requireManagedSpecReceipt(spec); err != nil {
			return err
		}
		if spec.ManagedSkills.TaskID != snapshot.TaskID || spec.ManagedSkills.SnapshotInputDigest != digest {
			return errors.New("skill preparation differs from original managed spec input")
		}
	}
	binding := skillmanager.SnapshotBinding{
		UserID: snapshot.UserID, AccountID: snapshot.AccountID, NodeID: snapshot.NodeID,
		SessionID: snapshot.SessionID, SnapshotID: snapshot.SnapshotID,
		DirectoryEpoch: snapshot.DirectoryEpoch, LibraryGeneration: snapshot.LibraryGeneration,
		InitialTreeDigest: snapshot.TreeDigest, TaskID: snapshot.TaskID, PreparationDigest: digest, SystemReleases: pins,
	}
	return e.prepareNativeSkillSnapshot(ctx, spec, binding, snapshot.Manifest, open)
}
