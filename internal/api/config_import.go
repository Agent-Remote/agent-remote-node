package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// ConfigImportAuthorization binds current directory ownership to one leased import task.
type ConfigImportAuthorization struct {
	TaskID         string `json:"task_id"`
	NodeID         string `json:"node_id"`
	UserID         string `json:"user_id"`
	AccountID      string `json:"account_id"`
	DirectoryMode  string `json:"directory_mode"`
	DirectoryEpoch int64  `json:"directory_epoch"`
}

// AuthorizeConfigImport rechecks ownership before any queued configuration is written.
func (c Client) AuthorizeConfigImport(ctx context.Context, taskID string) (ConfigImportAuthorization, error) {
	var response struct {
		Data struct {
			TaskID         string `json:"task_id"`
			NodeID         string `json:"node_id"`
			UserID         string `json:"user_id"`
			AccountID      string `json:"account_id"`
			DirectoryMode  string `json:"directory_mode"`
			DirectoryEpoch *int64 `json:"directory_epoch"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/node-api/tasks/"+url.PathEscape(taskID)+"/config-import-authorization", nil, &response, true)
	if err != nil {
		return ConfigImportAuthorization{}, err
	}
	data := response.Data
	if data.DirectoryEpoch == nil {
		return ConfigImportAuthorization{}, errors.New("config import authorization lacks directory epoch")
	}
	grant := ConfigImportAuthorization{
		TaskID: data.TaskID, NodeID: data.NodeID, UserID: data.UserID,
		AccountID: data.AccountID, DirectoryMode: data.DirectoryMode, DirectoryEpoch: *data.DirectoryEpoch,
	}
	validMode := grant.DirectoryMode == "legacy" || grant.DirectoryMode == "migrating" || grant.DirectoryMode == "managed_v1"
	if grant.TaskID != taskID || grant.NodeID == "" || grant.UserID == "" || grant.AccountID == "" || !validMode || grant.DirectoryEpoch < 0 || (grant.DirectoryMode != "legacy" && grant.DirectoryEpoch == 0) {
		return ConfigImportAuthorization{}, errors.New("config import authorization is incomplete")
	}
	return grant, nil
}
