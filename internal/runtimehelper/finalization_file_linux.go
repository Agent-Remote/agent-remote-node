package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) openFinalizationFile(ctx context.Context, request Request) (*os.File, finalizationFileResponse, error) {
	payload, err := validateFinalizationFileRequest(ctx, request, e.config.NodeID)
	if err != nil {
		return nil, finalizationFileResponse{}, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, finalizationFileResponse{}, err
	}
	defer store.Close()
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, payload.Binding.SessionID)
	if err != nil {
		return nil, finalizationFileResponse{}, err
	}
	defer bundle.Close()
	if session.Snapshot.Binding != payload.Binding || session.Runtime.Backend != "native" || session.Runtime.ResourceID != "agent-remote-session-"+shortDigest(payload.Binding.SessionID, 12)+".service" {
		return nil, finalizationFileResponse{}, errors.New("finalization differs from original Native session")
	}
	if payload.Kind == "hold" {
		file, record, err := skillmanager.HoldFinalizationContent(bundle, payload.Binding)
		if err != nil {
			return nil, finalizationFileResponse{}, err
		}
		metadata, err := finalizationFileMetadata(file, record, "hold", nil)
		if err != nil {
			_ = file.Close()
			return nil, finalizationFileResponse{}, err
		}
		return file, metadata, nil
	}
	file, record, err := skillmanager.OpenFinalizationManifest(bundle, payload.Binding)
	if err != nil {
		return nil, finalizationFileResponse{}, err
	}
	var entry *skillmanager.Entry
	if payload.Kind == "object" {
		_ = file.Close()
		if record.TreeDigest != payload.TreeDigest || record.Unclean != *payload.Unclean {
			return nil, finalizationFileResponse{}, errors.New("finalization object changed original input")
		}
		var selected skillmanager.Entry
		file, selected, err = skillmanager.OpenFinalizationObject(bundle, record, payload.Digest)
		if err != nil {
			return nil, finalizationFileResponse{}, err
		}
		entry = &selected
	}
	metadata, err := finalizationFileMetadata(file, record, payload.Kind, entry)
	if err != nil || ctx.Err() != nil {
		_ = file.Close()
		return nil, finalizationFileResponse{}, errors.Join(err, ctx.Err())
	}
	return file, metadata, nil
}
