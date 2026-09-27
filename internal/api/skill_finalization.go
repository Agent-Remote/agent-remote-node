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

// BeginSkillFinalization sends the immutable retained manifest; only an identical expired upload can renew.
// Passing a prior receipt prevents a retry from silently accepting a different finalization or older attempt.
func (c Client) BeginSkillFinalization(ctx context.Context, input SkillFinalizationInput, manifest skillmanager.Manifest, previous *SkillFinalization) (SkillFinalization, error) {
	if err := input.Validate(); err != nil {
		return SkillFinalization{}, err
	}
	if previous != nil {
		if err := previous.ValidateReceipt(input, nil); err != nil {
			return SkillFinalization{}, err
		}
	}
	digest, err := skillmanager.Digest(manifest)
	if err != nil || digest != input.TreeDigest {
		return SkillFinalization{}, errors.New("finalization manifest differs from retained capture")
	}
	payload := struct {
		SessionID      string                `json:"session_id"`
		IdempotencyKey string                `json:"idempotency_key"`
		Manifest       skillmanager.Manifest `json:"manifest"`
		Unclean        bool                  `json:"unclean"`
	}{input.SessionID, input.IdempotencyKey, manifest, input.Unclean}
	body, err := json.Marshal(payload)
	if err != nil {
		return SkillFinalization{}, err
	}
	if len(body) > maxSkillCaptureBytes {
		return SkillFinalization{}, errors.New("finalization manifest exceeds transport limit")
	}
	return c.finalizationRequest(ctx, http.MethodPost, "/api/v1/node/skill-snapshots/"+input.SnapshotID+"/finalization", input, previous, bytes.NewReader(body))
}

// GetSkillFinalization inspects the original receipt without renewing its upload attempt.
func (c Client) GetSkillFinalization(ctx context.Context, input SkillFinalizationInput, previous SkillFinalization) (SkillFinalization, error) {
	if err := previous.ValidateReceipt(input, nil); err != nil {
		return SkillFinalization{}, err
	}
	return c.finalizationRequest(ctx, http.MethodGet, finalizationPath(previous, "", false), input, &previous, nil)
}

// CompleteSkillFinalization acknowledges the exact upload's retained input, never account publication alone.
func (c Client) CompleteSkillFinalization(ctx context.Context, input SkillFinalizationInput, previous SkillFinalization) (SkillFinalization, error) {
	if err := previous.ValidateReceipt(input, nil); err != nil {
		return SkillFinalization{}, err
	}
	view, err := c.finalizationRequest(ctx, http.MethodPost, finalizationPath(previous, "/complete", true), input, &previous, nil)
	if err != nil {
		return SkillFinalization{}, err
	}
	if view.Status == "upload_pending" || view.UploadID != previous.UploadID || view.UploadAttempt != previous.UploadAttempt {
		return SkillFinalization{}, errors.New("finalization completion did not retain the exact upload")
	}
	return view, nil
}

// PublishSkillFinalization requests the existing whole-input decision without selecting conflict sides.
// Its receipt alone does not authorize local cleanup or prove that a conflict was resolved.
func (c Client) PublishSkillFinalization(ctx context.Context, input SkillFinalizationInput, retained SkillFinalization) (SkillPublication, error) {
	if err := retained.ValidateReceipt(input, nil); err != nil {
		return SkillPublication{}, err
	}
	if retained.CheckpointID == nil {
		return SkillPublication{}, errors.New("cannot publish an incomplete finalization")
	}
	var response skillEnvelope[SkillPublication]
	if err := c.skillRequest(ctx, http.MethodPost, finalizationPath(retained, "/publish", false), "application/json", nil, &response); err != nil {
		return SkillPublication{}, err
	}
	view := response.Data
	if response.SchemaVersion != 1 || response.Committed == nil || !*response.Committed || response.Status != view.Status || len(response.Errors) != 0 {
		return SkillPublication{}, errors.New("publication receipt differs from retained finalization")
	}
	if err := view.ValidateReceipt(input, retained); err != nil {
		return SkillPublication{}, err
	}
	return view, nil
}

func (c Client) finalizationRequest(ctx context.Context, method, path string, input SkillFinalizationInput, previous *SkillFinalization, body io.Reader) (SkillFinalization, error) {
	var response skillEnvelope[SkillFinalization]
	if err := c.skillRequest(ctx, method, path, "application/json", body, &response); err != nil {
		return SkillFinalization{}, err
	}
	view := response.Data
	if response.SchemaVersion != 1 || response.Status != view.Status || response.Committed == nil || *response.Committed != (view.Status != "upload_pending") || len(response.Errors) != 0 {
		return SkillFinalization{}, errors.New("inconsistent finalization response envelope")
	}
	if err := view.ValidateReceipt(input, previous); err != nil {
		return SkillFinalization{}, err
	}
	return view, nil
}

func finalizationPath(view SkillFinalization, suffix string, upload bool) string {
	path := "/api/v1/node/skill-finalizations/" + view.ID + suffix
	if upload {
		path += "?" + url.Values{"upload_id": {view.UploadID}}.Encode()
	}
	return path
}
