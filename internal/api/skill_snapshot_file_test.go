package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func snapshotFileHeaders(w http.ResponseWriter, entry skillmanager.Entry) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
	w.Header().Set("ETag", `"`+entry.SHA256+`"`)
}

func TestSkillSnapshotFileStreamsExactAuthorizedContent(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(strconv.FormatBool(binary), func(t *testing.T) {
			content := bytes.Repeat([]byte("学习 without normalization\r\n"), 100_000)
			if binary {
				content = append(content, 0xff, 0)
			}
			view, entry := snapshotTestView(t, content)
			if binary {
				entry.ContentKind = "binary"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer node-token" || r.Header.Get("Accept-Encoding") != "identity" ||
					r.URL.Path != "/api/v1/node/skill-snapshots/"+view.SnapshotID+"/files/"+entry.SHA256 || r.URL.Query().Get("task_id") != view.TaskID {
					t.Error("download lost exact task, snapshot or content identity")
				}
				snapshotFileHeaders(w, entry)
				_, _ = w.Write(content)
			}))
			defer server.Close()
			var target bytes.Buffer
			if err := NewClient(server.URL, "node-token").ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, &target); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(target.Bytes(), content) {
				t.Fatal("download changed original bytes")
			}
		})
	}
}

func TestSkillSnapshotFileRejectsHeadersTruncationAndContentMismatch(t *testing.T) {
	for _, kind := range []string{"etag", "weak etag", "length", "type", "encoding", "partial", "truncated", "digest", "classification"} {
		t.Run(kind, func(t *testing.T) {
			content := []byte("original content")
			view, entry := snapshotTestView(t, content)
			if kind == "classification" {
				entry.ContentKind = "binary"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				snapshotFileHeaders(w, entry)
				switch kind {
				case "etag":
					w.Header().Set("ETag", `"`+strings.Repeat("0", 64)+`"`)
				case "weak etag":
					w.Header().Set("ETag", `W/"`+entry.SHA256+`"`)
				case "length":
					w.Header().Set("Content-Length", strconv.FormatInt(entry.Size+1, 10))
				case "type":
					w.Header().Set("Content-Type", "text/html")
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "partial":
					w.WriteHeader(http.StatusPartialContent)
				case "truncated":
					content = content[:len(content)-1]
				case "digest":
					content = bytes.Repeat([]byte("x"), len(content))
				}
				_, _ = w.Write(content)
			}))
			defer server.Close()
			var target bytes.Buffer
			if err := NewClient(server.URL, "node-token").ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, &target); err == nil {
				t.Fatal("invalid file was accepted")
			}
			if kind != "truncated" && kind != "digest" && kind != "classification" && target.Len() != 0 {
				t.Fatal("unvalidated headers reached staging")
			}
		})
	}
}

func TestSkillSnapshotReadsRefuseRedirectsAndPreserveContentSafeErrors(t *testing.T) {
	view, entry := snapshotTestView(t, []byte("instructions"))
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(http.StatusOK) }))
	defer destination.Close()
	for _, redirect := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirect {
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"errors":[{"code":"SNAPSHOT_NOT_FOUND","message":"private body must not escape"}]}`)
		}))
		client := NewClient(server.URL, "secret-node-token")
		_, manifestErr := client.GetSkillSnapshot(context.Background(), view.SkillSnapshotIdentity)
		fileErr := client.ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, io.Discard)
		server.Close()
		for _, err := range []error{manifestErr, fileErr} {
			var response *HTTPError
			if !errors.As(err, &response) || strings.Contains(err.Error(), "private body") || strings.Contains(err.Error(), "secret-node-token") {
				t.Fatalf("unsafe HTTP failure: %v", err)
			}
			if !redirect && response.Code != "SNAPSHOT_NOT_FOUND" {
				t.Fatal("authorization denial code was lost")
			}
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("content request followed a redirect")
	}
}

func TestSkillSnapshotFileCancellationClosesBlockedBody(t *testing.T) {
	view, entry := snapshotTestView(t, []byte("private instructions"))
	started, closed := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshotFileHeaders(w, entry)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewClient(server.URL, "node-token").ReadSkillSnapshotFile(ctx, view.SkillSnapshotIdentity, entry, io.Discard)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop blocked download")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancellation retained the HTTP body")
	}
}

type snapshotRoundTripper func(*http.Request) (*http.Response, error)

func (f snapshotRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSkillSnapshotFileRejectsExtraBodyAndWriterFailure(t *testing.T) {
	view, entry := snapshotTestView(t, []byte("instructions"))
	for _, excess := range []bool{false, true} {
		client := NewClient("https://unused.invalid", "node-token")
		client.httpClient = &http.Client{Transport: snapshotRoundTripper(func(_ *http.Request) (*http.Response, error) {
			body := "instructions"
			if excess {
				body += "extra"
			}
			return &http.Response{StatusCode: 200, ContentLength: entry.Size, Header: http.Header{
				"Content-Type": []string{"application/octet-stream"}, "Etag": []string{`"` + entry.SHA256 + `"`},
			}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		var target io.Writer = io.Discard
		if !excess {
			target = snapshotFailedWriter{}
		}
		if err := client.ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, target); err == nil {
			t.Fatal("incomplete staged write was accepted")
		}
	}
}

type snapshotFailedWriter struct{}

func (snapshotFailedWriter) Write(_ []byte) (int, error) { return 0, io.ErrShortWrite }

func TestSkillSnapshotEmptyFileRequiresActualEOF(t *testing.T) {
	view, entry := snapshotTestView(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		snapshotFileHeaders(w, entry)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := NewClient(server.URL, "node-token").ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestSkillSnapshotRejectsUnboundInputsBeforeHTTP(t *testing.T) {
	view, entry := snapshotTestView(t, []byte("instructions"))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	invalid := view.SkillSnapshotIdentity
	invalid.TaskID = "logical-task-is-not-a-record-uuid"
	if _, err := client.GetSkillSnapshot(context.Background(), invalid); err == nil {
		t.Fatal("unbound snapshot requested")
	}
	if err := client.ReadSkillSnapshotFile(context.Background(), invalid, entry, io.Discard); err == nil {
		t.Fatal("unbound file requested")
	}
	if err := client.ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, nil); err == nil {
		t.Fatal("absent staging accepted")
	}
	entry.Kind = "symlink"
	if err := client.ReadSkillSnapshotFile(context.Background(), view.SkillSnapshotIdentity, entry, io.Discard); err == nil {
		t.Fatal("non-file requested")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid inputs reached HTTP")
	}
}
