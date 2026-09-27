package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type finalizationFileReceipt struct {
	UploadID string `json:"upload_id"`
	Digest   string `json:"digest"`
	Created  bool   `json:"created"`
}

func (r *finalizationFileReceipt) UnmarshalJSON(data []byte) error {
	type plain finalizationFileReceipt
	var decoded plain
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 3 {
		return errors.New("invalid file receipt fields")
	}
	for _, name := range []string{"upload_id", "digest", "created"} {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing file receipt field")
		}
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = finalizationFileReceipt(decoded)
	return nil
}

// PutSkillFinalizationFile consumes and closes one retained stream for the exact current upload.
// The Server checks manifest membership; both sides verify length, digest and text classification.
func (c Client) PutSkillFinalizationFile(ctx context.Context, input SkillFinalizationInput, upload SkillFinalization, entry skillmanager.Entry, source io.ReadCloser) error {
	if source == nil {
		return errors.New("finalization file source is absent")
	}
	reader := &skillUploadReader{source: source}
	defer reader.Close()
	if err := upload.ValidateReceipt(input, nil); err != nil {
		return err
	}
	if upload.Status != "upload_pending" || upload.UploadStatus != "staged" {
		return errors.New("finalization upload is not staged")
	}
	verifier, err := skillmanager.NewContentVerifier(entry)
	if err != nil {
		return err
	}
	reader.verifier, reader.remaining = verifier, entry.Size
	var response skillEnvelope[finalizationFileReceipt]
	if err := c.skillRequest(ctx, http.MethodPut, finalizationPath(upload, "/files/"+entry.SHA256, true), "application/octet-stream", reader, &response); err != nil {
		return err
	}
	if !reader.verified.Load() || response.SchemaVersion != 1 || response.Status != "upload_pending" || response.Committed == nil || *response.Committed || len(response.Errors) != 0 ||
		response.Data.UploadID != upload.UploadID || response.Data.Digest != entry.SHA256 {
		return errors.New("invalid finalization file acknowledgement")
	}
	return nil
}
