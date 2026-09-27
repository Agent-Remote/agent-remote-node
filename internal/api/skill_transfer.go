package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

const maxSkillResponseBytes = 4 << 20
const maxSkillCaptureBytes = 64 << 20

var skillErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)
var skillUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var skillDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validSkillUUID(value string) bool {
	return skillUUID.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

type skillEnvelope[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Committed     *bool  `json:"committed"`
	Data          T      `json:"data"`
	Errors        []struct {
		Code string `json:"code"`
	} `json:"errors"`
}

// skillRequest never follows redirects or retries an uncertain write. The caller owns retry identity.
func (c Client) skillRequest(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	return c.skillRequestLimit(ctx, method, path, contentType, body, out, maxSkillResponseBytes)
}

func (c Client) skillRequestLimit(ctx context.Context, method, path, contentType string, body io.Reader, out any, limit int64) error {
	req, err := c.newSkillRequest(ctx, method, path, contentType, body)
	if err != nil {
		return err
	}
	return c.sendSkillRequest(req, out, limit)
}

func (c Client) newSkillRequest(ctx context.Context, method, path, contentType string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.nodeToken)
	req.Header.Set("Content-Type", contentType)
	return req, nil
}

func (c Client) sendSkillRequest(req *http.Request, out any, limit int64) error {
	client := *c.httpClient
	client.Timeout = 10 * time.Minute
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = maxSkillResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return errors.New("skill response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope skillEnvelope[any]
		code := ""
		if decodeResponseJSON(data, &envelope) == nil && len(envelope.Errors) == 1 && skillErrorCode.MatchString(envelope.Errors[0].Code) {
			code = envelope.Errors[0].Code
		}
		return &HTTPError{StatusCode: resp.StatusCode, Code: code}
	}
	return decodeResponseJSON(data, out)
}

// skillUploadReader confirms EOF as well as declared length, so a prefix cannot be acknowledged.
type skillUploadReader struct {
	source    io.ReadCloser
	verifier  *skillmanager.ContentVerifier
	remaining int64
	verified  atomic.Bool
	closeOnce sync.Once
	closeErr  error
	stalls    int
}

func (r *skillUploadReader) Read(buffer []byte) (int, error) {
	if r.remaining < int64(len(buffer)) {
		buffer = buffer[:r.remaining+1]
	}
	n, err := r.source.Read(buffer)
	if n < 0 || n > len(buffer) {
		return 0, errors.New("invalid skill content reader")
	}
	if n > 0 {
		r.stalls = 0
		if _, verifyErr := r.verifier.Write(buffer[:n]); verifyErr != nil {
			return 0, verifyErr
		}
		r.remaining -= int64(n)
	} else if err == nil && len(buffer) > 0 {
		r.stalls++
		if r.stalls >= 100 {
			return 0, io.ErrNoProgress
		}
	}
	if errors.Is(err, io.EOF) {
		if verifyErr := r.verifier.Finish(); verifyErr != nil {
			return 0, verifyErr
		}
		r.verified.Store(true)
	}
	return n, err
}

func (r *skillUploadReader) Close() error {
	r.closeOnce.Do(func() { r.closeErr = r.source.Close() })
	return r.closeErr
}
