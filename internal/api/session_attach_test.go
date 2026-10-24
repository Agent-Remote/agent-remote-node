package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func exactAttachRequestFixture() VerifyExactAttachRequest {
	return VerifyExactAttachRequest{NodeID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", DeviceID: "33333333-3333-4333-8333-333333333333", SSHKeyID: "44444444-4444-4444-8444-444444444444"}
}

func exactAttachFieldsFixture() map[string]any {
	r := exactAttachRequestFixture()
	return map[string]any{"version": 1, "tool_type": "claude", "node_id": r.NodeID, "session_id": r.SessionID, "device_id": r.DeviceID, "ssh_key_id": r.SSHKeyID, "tmux_session_name": "tool-" + r.SessionID, "container_id": nil, "runtime_backend": "native", "runtime_resource_id": nil, "forward_ssh_agent": false}
}

func TestExactAttachRequiresCompleteUnambiguousTarget(t *testing.T) {
	type variant struct {
		name   string
		mutate func(map[string]any)
		raw    string
		valid  bool
	}
	cases := []variant{
		{name: "native", valid: true},
		{name: "docker_forward", valid: true, mutate: func(m map[string]any) {
			m["runtime_backend"] = "docker_sandbox"
			m["container_id"] = "sandbox-1"
			m["runtime_resource_id"] = "sandbox-1"
			m["forward_ssh_agent"] = true
		}},
		{name: "empty", raw: " "}, {name: "null", raw: "null"}, {name: "empty_envelope", raw: "{}"},
		{name: "duplicate_envelope", raw: `{"data":{},"data":{}}`},
		{name: "unknown", mutate: func(m map[string]any) { m["extra"] = true }},
		{name: "unsafe_tmux", mutate: func(m map[string]any) { m["tmux_session_name"] = "x;id" }},
		{name: "long_resource", mutate: func(m map[string]any) { m["runtime_resource_id"] = strings.Repeat("a", 257) }},
		{name: "unsafe_container", mutate: func(m map[string]any) { m["container_id"] = "../other" }},
		{name: "other_tool", mutate: func(m map[string]any) { m["tool_type"] = "codex" }},
		{name: "other_backend", mutate: func(m map[string]any) { m["runtime_backend"] = "shell" }},
		{name: "version_bool", mutate: func(m map[string]any) { m["version"] = true }},
		{name: "forward_string", mutate: func(m map[string]any) { m["forward_ssh_agent"] = "false" }},
	}
	for field := range exactAttachFieldsFixture() {
		cases = append(cases, variant{name: "missing_" + field, mutate: func(m map[string]any) { delete(m, field) }}, variant{name: "alias_" + field, mutate: func(m map[string]any) { m[strings.ToUpper(field)] = m[field]; delete(m, field) }})
		if field != "container_id" && field != "runtime_resource_id" {
			cases = append(cases, variant{name: "null_" + field, mutate: func(m map[string]any) { m[field] = nil }})
		}
	}
	for _, field := range []string{"session_id", "node_id", "device_id", "ssh_key_id"} {
		cases = append(cases, variant{name: "changed_" + field, mutate: func(m map[string]any) { m[field] = "55555555-5555-4555-8555-555555555555" }})
	}
	original, _ := json.Marshal(map[string]any{"data": exactAttachFieldsFixture()})
	cases = append(cases, variant{name: "aliased_envelope", raw: strings.Replace(string(original), `"data"`, `"Data"`, 1)}, variant{name: "unknown_envelope", raw: strings.Replace(string(original), `{"data"`, `{"extra":true,"data"`, 1)})
	cases = append(cases, variant{name: "duplicate_data_field", raw: strings.Replace(string(original), `"version":1`, `"version":0,"version":1`, 1)}, variant{name: "trailing", raw: string(original) + "{}"}, variant{name: "fraction_version", raw: strings.Replace(string(original), `"version":1`, `"version":1.0`, 1)})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/api/v1/node-api/attach/exact/verify" || r.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Error("wrong endpoint or authorization")
				}
				var body VerifyExactAttachRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body != exactAttachRequestFixture() {
					t.Errorf("wrong identity: %#v %v", body, err)
				}
				if test.raw != "" {
					_, _ = io.WriteString(w, test.raw)
					return
				}
				data := exactAttachFieldsFixture()
				if test.mutate != nil {
					test.mutate(data)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "request_id": "synthetic-request"})
			}))
			defer server.Close()
			response, err := NewClient(server.URL, "synthetic-token").VerifyExactAttach(context.Background(), exactAttachRequestFixture())
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, err=%v response=%#v", test.valid, err, response)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestExactAttachRejectsInvalidIdentityBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, invalid := range []string{"", "node-1", "00000000-0000-0000-0000-000000000000", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "11111111_1111-4111-8111-111111111111", "11111111-1111-4111-8111-11111111111z"} {
		for index := range 4 {
			request := exactAttachRequestFixture()
			fields := []*string{&request.NodeID, &request.SessionID, &request.DeviceID, &request.SSHKeyID}
			*fields[index] = invalid
			if _, err := NewClient(server.URL, "synthetic-token").VerifyExactAttach(context.Background(), request); err == nil {
				t.Errorf("invalid identity accepted: %q", invalid)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid identity reached network %d times", calls.Load())
	}
}

func TestExactAttachNeverFollowsRedirectOrDisclosesResponse(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusForbidden, http.StatusInternalServerError} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-material"}}`)
		}))
		_, err := NewClient(server.URL, "synthetic-token").VerifyExactAttach(context.Background(), exactAttachRequestFixture())
		server.Close()
		if err == nil || strings.Contains(err.Error(), "synthetic-private-material") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
