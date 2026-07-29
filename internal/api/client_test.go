package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedeemPortForwardUsesNodeAuthenticationAndTypedContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/node-api/port-forwards/redeem" {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
		if request.Header.Get("authorization") != "Bearer node-token" {
			t.Fatalf("unexpected authorization header")
		}
		var payload RedeemPortForwardRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ConnectToken != "one-time-token" || payload.SSHKeyID != "key-1" {
			t.Fatalf("unexpected redeem payload: %#v", payload)
		}
		_, _ = io.WriteString(response, `{"data":{"forward_id":"forward-1","session_id":"session-1","runtime_backend":"native","runtime_resource_id":"unit-1","remote_port":5173,"generation":1,"lease_expires_at":"2026-07-30T00:00:00Z","max_streams":128,"bytes_per_second":0,"control_plane_grace_seconds":300}}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	result, err := client.RedeemPortForward(context.Background(), RedeemPortForwardRequest{
		ForwardID: "forward-1", DeviceID: "device-1", SSHKeyID: "key-1", ConnectToken: "one-time-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data.RemotePort != 5173 || result.Data.ControlPlaneGraceSeconds != 300 {
		t.Fatalf("unexpected lease: %#v", result.Data)
	}
}

func TestHTTPErrorDoesNotExposeResponseOrCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(response, `{"error":{"code":"AUTH_INVALID","message":"reflected one-time-token"}}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	_, err := client.RedeemPortForward(context.Background(), RedeemPortForwardRequest{ConnectToken: "one-time-token"})
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.Code != "AUTH_INVALID" {
		t.Fatalf("expected typed HTTP error, got %v", err)
	}
	if strings.Contains(err.Error(), "one-time-token") || strings.Contains(err.Error(), "node-token") {
		t.Fatalf("error leaked a credential: %v", err)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, strings.Repeat("x", maxResponseBodyBytes+1))
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	if _, err := client.PollTasks(context.Background()); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected response size error, got %v", err)
	}
}

func TestPortForwardRenewReleaseAndUncodedHTTPError(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/api/v1/node-api/port-forwards/forward-1/renew":
			_, _ = io.WriteString(response, `{"data":{"forward_id":"forward-1","generation":1}}`)
		case "/api/v1/node-api/port-forwards/forward-1/release":
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-token")
	if _, err := client.RenewPortForward(context.Background(), "forward-1", RenewPortForwardRequest{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	if err := client.ReleasePortForward(context.Background(), "forward-1", ReleasePortForwardRequest{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := client.RedeemPortForward(context.Background(), RedeemPortForwardRequest{})
	if err == nil || err.Error() != "server returned HTTP 503" {
		t.Fatalf("unexpected uncoded HTTP error: %v", err)
	}
	if len(methods) != 3 {
		t.Fatalf("unexpected requests: %v", methods)
	}
}
