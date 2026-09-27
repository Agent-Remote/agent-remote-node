package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigImportAuthorizationRequiresFreshCompleteResponse(t *testing.T) {
	for _, mode := range []string{"legacy", "managed_v1", "migrating", "unknown", ""} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/node-api/tasks/import:one/config-import-authorization" || r.Header.Get("Authorization") != "Bearer node-token" {
					t.Errorf("unexpected authorization request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = fmt.Fprintf(w, `{"data":{"task_id":"import:one","node_id":"node","user_id":"user","account_id":"account","directory_mode":%q,"directory_epoch":1}}`, mode)
			}))
			defer server.Close()
			grant, err := NewClient(server.URL, "node-token").AuthorizeConfigImport(context.Background(), "import:one")
			if mode == "unknown" || mode == "" {
				if err == nil {
					t.Fatal("missing or future mode was accepted")
				}
			} else if err != nil || grant.DirectoryMode != mode {
				t.Fatalf("valid response failed: %#v %v", grant, err)
			}
		})
	}
}

func TestConfigImportAuthorizationCannotFallBackToOldOrMalformedResponses(t *testing.T) {
	for _, response := range []string{
		``, `{}`, `{"data":{}}`,
		`{"data":{"task_id":"import","node_id":"node","user_id":"user","account_id":"account","directory_mode":"legacy"}}`,
		`{"data":{"task_id":"other","node_id":"node","user_id":"user","account_id":"account","directory_mode":"legacy","directory_epoch":0}}`,
		`{"data":{"task_id":"import","node_id":"node","user_id":"user","account_id":"account","directory_mode":"managed_v1","directory_epoch":0}}`,
		`{"data":{"task_id":"import","node_id":"node","user_id":"user","account_id":"account","directory_mode":"legacy","directory_epoch":true}}`,
	} {
		t.Run(response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(response)) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "node-token").AuthorizeConfigImport(context.Background(), "import"); err == nil {
				t.Fatal("malformed response was accepted")
			}
		})
	}
}

func TestTaskPollingSupportsLargeImportsWithoutWideningOtherResponses(t *testing.T) {
	padding := strings.Repeat("x", maxResponseBodyBytes+64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/node-api/tasks/poll" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"tasks": []any{map[string]any{"task_id": "import", "task_type": "import_tool_account_config", "payload": map[string]any{"content": padding}}}}})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": padding})
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	response, err := client.PollTasks(context.Background())
	if err != nil || len(response.Data.Tasks) != 1 {
		t.Fatalf("legal large task could not be polled: %v", err)
	}
	if _, err := client.AuthorizeConfigImport(context.Background(), "import"); err == nil {
		t.Fatal("small authorization endpoint accepted a large response")
	}
}
