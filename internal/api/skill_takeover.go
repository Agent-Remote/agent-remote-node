package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// SkillTakeover is the original authority receipt, not permission to remove retained source data.
type SkillTakeover struct {
	ProtocolVersion int                          `json:"protocol_version"`
	ManifestVersion int                          `json:"manifest_version"`
	TakeoverID      string                       `json:"takeover_id"`
	TaskID          string                       `json:"task_id"`
	NodeID          string                       `json:"node_id"`
	UserID          string                       `json:"user_id"`
	AccountID       string                       `json:"account_id"`
	RuntimeBackend  string                       `json:"runtime_backend"`
	DirectoryEpoch  int64                        `json:"directory_epoch"`
	InventoryDigest string                       `json:"inventory_digest"`
	Inventory       []skillmanager.AccountWriter `json:"inventory"`
	Status          string                       `json:"status"`
	HelperReceiptID *string                      `json:"helper_receipt_id"`
	CaptureDigest   *string                      `json:"capture_digest"`
	UploadID        *string                      `json:"upload_id"`
	UploadAttempt   int64                        `json:"upload_attempt"`
	CheckpointID    *string                      `json:"checkpoint_id"`
}

// GetSkillTakeover refreshes exact task authorization even while old writers are still draining.
func (c Client) GetSkillTakeover(ctx context.Context, binding skillmanager.AccountTakeoverBinding) (SkillTakeover, error) {
	return c.takeoverRequest(ctx, http.MethodGet, binding, "", nil, "")
}

// BeginSkillTakeover fixes the Helper's immutable capture and renews only identical expired input.
func (c Client) BeginSkillTakeover(ctx context.Context, capture skillmanager.AccountCapture, manifest skillmanager.Manifest) (SkillTakeover, error) {
	digest, err := skillmanager.Digest(manifest)
	if err != nil {
		return SkillTakeover{}, err
	}
	if capture.Version != 1 || !validSkillUUID(capture.HelperReceiptID) || capture.TreeDigest != digest {
		return SkillTakeover{}, errors.New("invalid takeover capture identity")
	}
	payload := struct {
		HelperReceiptID  string                `json:"helper_receipt_id"`
		DirectoryEpoch   int64                 `json:"directory_epoch"`
		InventoryDigest  string                `json:"inventory_digest"`
		WritersQuiescent bool                  `json:"writers_quiescent"`
		Manifest         skillmanager.Manifest `json:"manifest"`
	}{capture.HelperReceiptID, capture.Binding.DirectoryEpoch, capture.Binding.InventoryDigest, true, manifest}
	data, err := json.Marshal(payload)
	if err != nil {
		return SkillTakeover{}, err
	}
	if len(data) > maxSkillCaptureBytes {
		return SkillTakeover{}, errors.New("takeover capture exceeds transport limit")
	}
	result, err := c.takeoverRequest(ctx, http.MethodPost, capture.Binding, "/capture", bytes.NewReader(data), "")
	if err != nil {
		return SkillTakeover{}, err
	}
	if result.Status == "reserved" || result.HelperReceiptID == nil || *result.HelperReceiptID != capture.HelperReceiptID || result.CaptureDigest == nil || *result.CaptureDigest != digest {
		return SkillTakeover{}, errors.New("server acknowledged another takeover capture")
	}
	return result, nil
}

// PutSkillTakeoverFile consumes and closes one retained file, checking streamed bytes before acknowledgement.
func (c Client) PutSkillTakeoverFile(ctx context.Context, binding skillmanager.AccountTakeoverBinding, uploadID string, entry skillmanager.Entry, source io.ReadCloser) error {
	if source == nil {
		return errors.New("takeover file source is absent")
	}
	reader := &skillUploadReader{source: source}
	defer reader.Close()
	if err := binding.Validate(); err != nil {
		return err
	}
	if !validSkillUUID(uploadID) {
		return errors.New("invalid takeover upload identity")
	}
	verifier, err := skillmanager.NewContentVerifier(entry)
	if err != nil {
		return err
	}
	reader.verifier, reader.remaining = verifier, entry.Size
	var response skillEnvelope[struct {
		UploadID string `json:"upload_id"`
		Digest   string `json:"digest"`
		Created  *bool  `json:"created"`
	}]
	err = c.skillRequest(ctx, http.MethodPut, takeoverPath(binding, "/files/"+entry.SHA256, uploadID), "application/octet-stream", reader, &response)
	if err != nil {
		return err
	}
	if !reader.verified.Load() || response.SchemaVersion != 1 || response.Status != "upload_pending" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 ||
		response.Data.UploadID != uploadID || response.Data.Digest != entry.SHA256 || response.Data.Created == nil {
		return errors.New("invalid takeover file acknowledgement")
	}
	return nil
}

// CompleteSkillTakeover acknowledges only the current upload's original committed checkpoint.
func (c Client) CompleteSkillTakeover(ctx context.Context, capture skillmanager.AccountCapture, uploadID string) (SkillTakeover, error) {
	if !validSkillUUID(uploadID) {
		return SkillTakeover{}, errors.New("invalid takeover upload identity")
	}
	if capture.Version != 1 || !validSkillUUID(capture.HelperReceiptID) || !skillDigest.MatchString(capture.TreeDigest) {
		return SkillTakeover{}, errors.New("invalid takeover capture identity")
	}
	result, err := c.takeoverRequest(ctx, http.MethodPost, capture.Binding, "/complete", nil, uploadID)
	if err != nil {
		return SkillTakeover{}, err
	}
	if result.Status != "committed" || result.UploadID == nil || *result.UploadID != uploadID ||
		result.HelperReceiptID == nil || *result.HelperReceiptID != capture.HelperReceiptID || result.CaptureDigest == nil || *result.CaptureDigest != capture.TreeDigest {
		return SkillTakeover{}, errors.New("takeover completion did not acknowledge the exact upload")
	}
	return result, nil
}

func (c Client) takeoverRequest(ctx context.Context, method string, binding skillmanager.AccountTakeoverBinding, suffix string, body io.Reader, uploadID string) (SkillTakeover, error) {
	if err := binding.Validate(); err != nil {
		return SkillTakeover{}, err
	}
	var response skillEnvelope[SkillTakeover]
	if err := c.skillRequest(ctx, method, takeoverPath(binding, suffix, uploadID), "application/json", body, &response); err != nil {
		return SkillTakeover{}, err
	}
	if err := validateTakeoverResponse(response, binding); err != nil {
		return SkillTakeover{}, err
	}
	return response.Data, nil
}

func takeoverPath(binding skillmanager.AccountTakeoverBinding, suffix, uploadID string) string {
	query := url.Values{"task_id": {binding.TaskID}}
	if uploadID != "" {
		query.Set("upload_id", uploadID)
	}
	return "/api/v1/node/skill-takeovers/" + binding.TakeoverID + suffix + "?" + query.Encode()
}

func validateTakeoverResponse(response skillEnvelope[SkillTakeover], expected skillmanager.AccountTakeoverBinding) error {
	view := response.Data
	binding := skillmanager.AccountTakeoverBinding{
		Version: view.ProtocolVersion, NodeID: view.NodeID, UserID: view.UserID, AccountID: view.AccountID,
		TakeoverID: view.TakeoverID, TaskID: view.TaskID, RuntimeBackend: view.RuntimeBackend,
		DirectoryEpoch: view.DirectoryEpoch, InventoryDigest: view.InventoryDigest,
	}
	digest, err := skillmanager.AccountInventoryDigest(view.Inventory)
	if err != nil {
		return err
	}
	if response.SchemaVersion != 1 || response.Committed == nil || response.Status != view.Status || len(response.Errors) != 0 || binding != expected ||
		view.ManifestVersion != 1 || view.Inventory == nil || digest != expected.InventoryDigest {
		return errors.New("takeover response differs from the exact reservation")
	}
	for _, writer := range view.Inventory {
		if writer.NodeID != expected.NodeID {
			return errors.New("takeover inventory contains another node")
		}
	}
	switch view.Status {
	case "reserved":
		if *response.Committed || view.HelperReceiptID != nil || view.CaptureDigest != nil || view.UploadID != nil || view.UploadAttempt != 0 || view.CheckpointID != nil {
			return errors.New("invalid reserved takeover receipt")
		}
	case "uploading", "committed":
		if view.HelperReceiptID == nil || !validSkillUUID(*view.HelperReceiptID) || view.CaptureDigest == nil || !skillDigest.MatchString(*view.CaptureDigest) || view.UploadID == nil || !validSkillUUID(*view.UploadID) || view.UploadAttempt <= 0 {
			return errors.New("incomplete takeover capture receipt")
		}
		if view.Status == "committed" {
			if !*response.Committed || view.CheckpointID == nil || !validSkillUUID(*view.CheckpointID) {
				return errors.New("missing takeover authority checkpoint")
			}
		} else if *response.Committed || view.CheckpointID != nil {
			return errors.New("premature takeover authority acknowledgement")
		}
	default:
		return errors.New("unknown takeover status")
	}
	return nil
}
