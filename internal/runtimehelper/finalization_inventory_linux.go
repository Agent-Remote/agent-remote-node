package runtimehelper

import (
	"context"
	"errors"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (e Engine) listFinalizations(ctx context.Context, request Request) (skillmanager.FinalizationPage, error) {
	page := skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{}}
	input, err := validateFinalizationListRequest(ctx, request, e.config.NodeID)
	if err != nil {
		return page, err
	}
	store, err := e.openExistingSkillStateRoot()
	if errors.Is(err, skillmanager.ErrStateStoreAbsent) {
		return page, nil
	}
	if err != nil {
		return page, err
	}
	defer store.Close()
	ids, more, invalid, err := skillmanager.ListFinalizationSessions(ctx, store, input.Cursor)
	if err != nil {
		return page, err
	}
	page.InvalidNames = invalid
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		page.Items = append(page.Items, e.inspectFinalizationInventory(store, id))
	}
	if more {
		page.NextCursor = ids[len(ids)-1]
	}
	return page, page.Validate(input.Cursor, input.NodeID)
}

func (e Engine) inspectFinalization(ctx context.Context, request Request) (skillmanager.FinalizationPage, error) {
	page := skillmanager.FinalizationPage{Items: []skillmanager.FinalizationInventoryItem{}}
	input, err := validateFinalizationInspect(ctx, request, e.config.NodeID)
	if err != nil {
		return page, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return page, err
	}
	defer store.Close()
	page.Items = append(page.Items, e.inspectFinalizationInventory(store, input.SessionID))
	return page, page.Validate("", input.NodeID)
}

func (e Engine) inspectFinalizationInventory(store *os.Root, sessionID string) skillmanager.FinalizationInventoryItem {
	item := skillmanager.FinalizationInventoryItem{SessionID: sessionID, Code: "invalid_retained_state"}
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, sessionID)
	if err != nil {
		return item
	}
	defer bundle.Close()
	binding := session.Snapshot.Binding
	if binding.NodeID != e.config.NodeID {
		return item
	}
	if session.Runtime.Backend != "native" {
		item.Code = "unsupported_backend"
		return item
	}
	if session.Runtime.ResourceID != "agent-remote-session-"+shortDigest(sessionID, 12)+".service" {
		return item
	}
	// Only absence of the finalization directory means unfinished capture. Missing files within
	// an existing directory, links and unreadable metadata must remain explicit retained errors.
	if _, err := bundle.Lstat("finalization"); errors.Is(err, os.ErrNotExist) {
		item.Code = "not_finalized"
		return item
	} else if err != nil {
		return item
	}
	record, err := skillmanager.ReadFinalization(bundle, binding)
	if err != nil {
		return item
	}
	intent, complete, err := skillmanager.ReadFinalizationReclamation(bundle, binding)
	if err != nil {
		return item
	}
	if intent != nil {
		item.Code = "reclamation_pending"
		if complete {
			item.Code = "content_reclaimed"
		}
		return item
	}
	item.Record, item.Code = &record, ""
	return item
}
