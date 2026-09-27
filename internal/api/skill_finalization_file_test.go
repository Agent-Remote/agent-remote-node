package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSkillFinalizationFileRejectsCorruptionAndWrongAcknowledgements(t *testing.T) {
	input, manifest, upload := finalizationFixture()
	entry := manifest.Entries[0]
	original := []byte{0xff, 0, 'a', 'b'}
	for _, change := range []string{"truncated", "extra_bytes", "modified", "classification", "upload", "digest", "created_missing", "created_null", "created_alias", "extra_field", "committed", "status"} {
		t.Run(change, func(t *testing.T) {
			content, file := bytes.Clone(original), entry
			switch change {
			case "truncated":
				content = content[:3]
			case "extra_bytes":
				content = append(content, 'x')
			case "modified":
				content[3] = 'x'
			case "classification":
				file.ContentKind = "text"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				fields := map[string]any{"upload_id": upload.UploadID, "digest": entry.SHA256, "created": true}
				envelope := map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false, "data": fields}
				switch change {
				case "upload":
					fields["upload_id"] = takeoverTestHelper
				case "digest":
					fields["digest"] = strings.Repeat("a", 64)
				case "created_missing":
					delete(fields, "created")
				case "created_null":
					fields["created"] = nil
				case "created_alias":
					fields["Created"] = fields["created"]
					delete(fields, "created")
				case "extra_field":
					fields["path"] = "/private"
				case "committed":
					envelope["committed"] = true
				case "status":
					envelope["status"] = "persisted"
				}
				_ = json.NewEncoder(w).Encode(envelope)
			}))
			defer server.Close()
			source := &takeoverTestSource{Reader: bytes.NewReader(content)}
			if err := NewClient(server.URL, "node").PutSkillFinalizationFile(context.Background(), input, upload, file, source); err == nil || source.closed.Load() != 1 {
				t.Fatal("bad stream or receipt accepted, or source leaked", err)
			}
		})
	}
}

func TestSkillFinalizationFileRejectsEarlySuccessAndClosesInvalidSources(t *testing.T) {
	input, manifest, upload := finalizationFixture()
	entry := manifest.Entries[0]
	calls := 0
	client := NewClient("https://example.invalid", "node")
	client.httpClient.Transport = takeoverRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		_ = r.Body.Close()
		data, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false,
			"data": map[string]any{"upload_id": upload.UploadID, "digest": entry.SHA256, "created": true}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})
	source := &takeoverTestSource{Reader: bytes.NewReader([]byte{0xff, 0, 'a', 'b'})}
	if err := client.PutSkillFinalizationFile(context.Background(), input, upload, entry, source); err == nil || source.closed.Load() != 1 || calls != 1 {
		t.Fatal("unconsumed file acknowledged", err)
	}
	upload.UploadStatus = "expired"
	source = &takeoverTestSource{Reader: bytes.NewReader(nil)}
	if err := client.PutSkillFinalizationFile(context.Background(), input, upload, entry, source); err == nil || source.closed.Load() != 1 || calls != 1 {
		t.Fatal("expired upload reached network or leaked source", err)
	}
}
