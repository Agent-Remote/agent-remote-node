package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// ReadSkillSnapshotFile verifies one original manifest entry while copying it to private staging.
// The caller must discard staging on any error; this method never publishes partial content.
func (c Client) ReadSkillSnapshotFile(ctx context.Context, binding SkillSnapshotIdentity, entry skillmanager.Entry, target io.Writer) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	return c.readSkillFile(ctx, snapshotPath(binding, "/files/"+entry.SHA256), entry, target)
}

// readSkillFile shares bounded verified bytes only; callers must independently validate their authority.
func (c Client) readSkillFile(ctx context.Context, path string, entry skillmanager.Entry, target io.Writer) error {
	verifier, err := skillmanager.NewContentVerifier(entry)
	if err != nil {
		return err
	}
	if target == nil {
		return errors.New("skill snapshot staging writer is absent")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.nodeToken)
	req.Header.Set("Accept-Encoding", "identity")
	client := *c.httpClient
	client.Timeout = 10 * time.Minute
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return snapshotFileError(response)
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	encoding := response.Header.Get("Content-Encoding")
	if err != nil || contentType != "application/octet-stream" || response.ContentLength != entry.Size ||
		response.Header.Get("ETag") != `"`+entry.SHA256+`"` || response.Uncompressed || encoding != "" && encoding != "identity" {
		return errors.New("invalid skill snapshot file response")
	}
	if _, err := io.Copy(io.MultiWriter(verifier, target), io.LimitReader(response.Body, entry.Size)); err != nil {
		return err
	}
	var excess [1]byte
	if count, err := io.ReadFull(response.Body, excess[:]); count != 0 || !errors.Is(err, io.EOF) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("skill snapshot file has excess or unreadable content")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return verifier.Finish()
}

func snapshotFileError(response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSkillResponseBytes+1))
	if err != nil {
		return err
	}
	code := ""
	if len(data) <= maxSkillResponseBytes {
		var envelope skillEnvelope[any]
		if decodeResponseJSON(data, &envelope) == nil && len(envelope.Errors) == 1 && skillErrorCode.MatchString(envelope.Errors[0].Code) {
			code = envelope.Errors[0].Code
		}
	}
	return &HTTPError{StatusCode: response.StatusCode, Code: code}
}
