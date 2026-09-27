package runtimehelper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
)

func (e Engine) importAccountConfig(ctx context.Context, request Request) (map[string]any, error) {
	var payload ConfigImportRequest
	if err := decodeStrictPayload(request.Payload, &payload); err != nil {
		return nil, errors.New("invalid config import helper payload")
	}
	if err := validateConfigImport(request.RequestID, payload); err != nil {
		return nil, err
	}
	if !validSkillUUID(e.config.NodeID) {
		return nil, errors.New("invalid configured Node identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	account := payload.Account
	accountPath := filepath.Join(e.config.AccountRoot, account.UserID, "tool-accounts", account.ToolType, account.ToolAccountID)
	input, err := json.Marshal(struct {
		Account toolaccounts.ImportConfigPayload
		Root    string
	}{account, accountPath})
	if err != nil || len(input) > 13<<20 {
		return nil, errors.New("invalid or oversized config import input")
	}
	digest := sha256.Sum256(input)
	expected := skillmanager.AccountImportReceipt{
		Version: 1, TaskID: request.RequestID, NodeID: e.config.NodeID, UserID: account.UserID,
		AccountID: account.ToolAccountID, InputDigest: hex.EncodeToString(digest[:]), State: "started",
	}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	saved, receiptErr := skillmanager.ReadAccountImport(store, expected)
	if receiptErr != nil && !errors.Is(receiptErr, os.ErrNotExist) {
		return nil, receiptErr
	}
	mode, err := e.configImportMode(store, payload)
	if err != nil {
		return nil, err
	}
	if receiptErr == nil {
		switch saved.State {
		case "succeeded":
			return configImportResult(accountPath, account), nil
		case "failed":
			return nil, errConfigImportFailed
		default:
			return nil, errConfigImportPending
		}
	}
	if err := skillmanager.CheckAccountMigrationHistory(store, e.config.NodeID, account.UserID, account.ToolAccountID); err != nil {
		return nil, errMigrationWritersUnknown
	}
	if err := toolaccounts.ValidateImportConfig(e.config.AccountRoot, account, mode); err != nil {
		if errors.Is(err, toolaccounts.ErrSkillManagerOwnsPath) {
			return nil, err
		}
		return nil, errors.New("invalid config import file batch")
	}
	identity, err := e.configImportIdentity(account)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := skillmanager.BeginAccountImport(store, expected); err != nil {
		return nil, err
	}
	_, writeErr := toolaccounts.ImportConfigAs(e.config.AccountRoot, account, mode, &toolaccounts.ImportOwnership{UID: identity.UID, GID: identity.GID})
	state := "succeeded"
	if writeErr != nil {
		state = "failed"
	}
	if err := skillmanager.FinishAccountImport(store, expected, state); err != nil {
		return nil, errConfigImportPending
	}
	if writeErr != nil {
		return nil, errConfigImportFailed
	}
	return configImportResult(accountPath, account), nil
}

func (e Engine) configImportMode(store *os.Root, request ConfigImportRequest) (string, error) {
	account := request.Account
	if request.DirectoryMode != "legacy" {
		_, err := skillmanager.CloseAccountImports(store, skillmanager.AccountFence{
			Version: 1, NodeID: e.config.NodeID, UserID: account.UserID, AccountID: account.ToolAccountID, DirectoryEpoch: request.DirectoryEpoch,
		})
		return request.DirectoryMode, err
	}
	_, err := skillmanager.ReadAccountFence(store, e.config.NodeID, account.UserID, account.ToolAccountID)
	if errors.Is(err, os.ErrNotExist) {
		return "legacy", nil
	}
	if err != nil {
		return "", err
	}
	return "managed_v1", nil
}

func (e Engine) configImportIdentity(account toolaccounts.ImportConfigPayload) (runtimeIdentity, error) {
	if account.RuntimeBackend == "docker_sandbox" {
		return e.dockerRuntimeIdentity()
	}
	identity, err := e.ensureIdentity(account.UserID)
	if err != nil {
		return runtimeIdentity{}, err
	}
	if identity.UID <= 0 || identity.GID <= 0 {
		return runtimeIdentity{}, errors.New("invalid config import runtime identity")
	}
	return identity, nil
}
