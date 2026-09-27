package runtimehelper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

func TestHelperCaptureDescriptorUploadsWithStreamVerification(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	helper, _ := serveCaptureTest(t, engine, os.Getuid())
	retained, manifest, err := helper.ReadAccountCapture(context.Background(), "upload-manifest", capture.Binding)
	if err != nil || retained != capture {
		t.Fatal("capture unavailable", err)
	}
	expected := manifest.Entries[0]
	uploadID := "88888888-8888-4888-8888-888888888888"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		if r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer capture-test-node" || r.URL.Query().Get("task_id") != capture.Binding.TaskID || r.URL.Query().Get("upload_id") != uploadID {
			t.Error("descriptor stream lost exact upload authorization")
		}
		content, readErr := io.ReadAll(r.Body)
		if call == 1 && (readErr != nil || string(content) != "retained\x00\xff") {
			t.Error("descriptor bytes changed", readErr)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "upload_pending", "committed": false,
			"data": map[string]any{"upload_id": uploadID, "digest": expected.SHA256, "created": true}})
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "capture-test-node")
	file, entry, err := helper.OpenAccountCaptureObject(context.Background(), "upload-object", capture, expected.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.PutSkillTakeoverFile(context.Background(), capture.Binding, uploadID, entry, file); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Stat(); err == nil {
		t.Fatal("upload did not close its adopted descriptor")
	}
	path := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID, "objects", expected.SHA256)
	if err := os.WriteFile(path, []byte("modified\x00\xff"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, entry, err = helper.OpenAccountCaptureObject(context.Background(), "damaged-object", capture, expected.SHA256)
	if err != nil {
		t.Fatal("same-length bytes should reach streaming verification", err)
	}
	if err := client.PutSkillTakeoverFile(context.Background(), capture.Binding, uploadID, entry, file); err == nil {
		t.Fatal("corrupted retained bytes were acknowledged")
	}
	if _, err := file.Stat(); err == nil {
		t.Fatal("failed upload leaked its descriptor")
	}
}
