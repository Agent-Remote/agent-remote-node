package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Closing a reused connection after reading the POST reproduces the uncertain EOF
// boundary without timing-dependent sleeps or replaying an authorization request.
func TestExportVerificationDoesNotRetainHTTPConnections(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "http"
		if secure {
			name = "private-tls"
		}
		t.Run(name, func(t *testing.T) {
			permission := exportPermissionFixture()
			var mu sync.Mutex
			connections := make(map[string]bool)
			calls := 0
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mu.Lock()
				calls++
				reused := connections[r.RemoteAddr]
				connections[r.RemoteAddr] = true
				mu.Unlock()
				if reused {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"schema_version": 1, "status": "authorized", "committed": false,
					"data": permission, "errors": []string{},
				})
			}))
			var client Client
			if secure {
				server.StartTLS()
				trust := server.Client().Transport.(*http.Transport).TLSClientConfig
				client = NewClientWithTLSConfig(server.URL, "export-node-fixture", trust)
			} else {
				server.Start()
				client = NewClient(server.URL, "export-node-fixture")
				// Keep this test's idle pool independent of other API tests.
				client.httpClient.Transport = http.DefaultTransport.(*http.Transport).Clone()
			}
			defer server.Close()
			defer client.httpClient.CloseIdleConnections()
			for range 3 {
				got, err := client.VerifyNodeExport(context.Background(), permission.Binding.NodeID,
					permission.Binding.SnapshotID, permission.DeviceID, permission.SSHKeyID,
					"fixture=."+strings.Repeat("a", 64))
				if err != nil || got != permission {
					t.Fatalf("original verification failed: %v", err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if calls != 3 || len(connections) != 3 {
				t.Fatalf("verification reused or replayed a connection: requests=%d connections=%d", calls, len(connections))
			}
		})
	}
}

func TestExportVerificationDoesNotReplayUncertainEOF(t *testing.T) {
	permission := exportPermissionFixture()
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		calls++
		mu.Unlock()
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	client := NewClient(server.URL, "export-node-fixture")
	_, err := client.VerifyNodeExport(context.Background(), permission.Binding.NodeID,
		permission.Binding.SnapshotID, permission.DeviceID, permission.SSHKeyID,
		"fixture=."+strings.Repeat("a", 64))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("uncertain verification did not fail with EOF: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("uncertain authorization replayed %d times", calls)
	}
}

func TestExportVerificationPreservesOrdinaryRequestPooling(t *testing.T) {
	permission := exportPermissionFixture()
	var mu sync.Mutex
	ordinaryConnections := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/ordinary" {
			mu.Lock()
			ordinaryConnections[r.RemoteAddr] = true
			mu.Unlock()
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": 1, "status": "authorized", "committed": false,
			"data": permission, "errors": []string{},
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "export-node-fixture")
	client.httpClient.Transport = http.DefaultTransport.(*http.Transport).Clone()
	defer client.httpClient.CloseIdleConnections()
	if _, err := client.VerifyNodeExport(context.Background(), permission.Binding.NodeID,
		permission.Binding.SnapshotID, permission.DeviceID, permission.SSHKeyID,
		"fixture=."+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var response struct{}
		if err := client.skillRequest(context.Background(), http.MethodGet, "/ordinary", "", nil, &response); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ordinaryConnections) != 1 {
		t.Fatal("export changed the shared transport's connection pooling")
	}
}
