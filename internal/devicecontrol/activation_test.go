package devicecontrol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testNodeID          = "123e4567-e89b-42d3-a456-426614174000"
	testDeviceSessionID = "223e4567-e89b-42d3-a456-426614174001"
	testToolSessionID   = "323e4567-e89b-42d3-a456-426614174002"
)

func validActivation(now time.Time) map[string]any {
	return map[string]any{
		"protocol_version":    1,
		"user_id":             "423e4567-e89b-42d3-a456-426614174003",
		"device_id":           "523e4567-e89b-42d3-a456-426614174004",
		"tool_session_id":     testToolSessionID,
		"device_session_id":   testDeviceSessionID,
		"node_id":             testNodeID,
		"platform":            "macos",
		"generation":          1,
		"expires_at":          now.Add(time.Hour).Format(time.RFC3339Nano),
		"runtime_backend":     "docker_sandbox",
		"runtime_resource_id": "session-runtime-1",
	}
}

// TestActivationIsStrictAtomicAndGenerationBound verifies atomic generation-bound activation state.
func TestActivationIsStrictAtomicAndGenerationBound(t *testing.T) {
	now := time.Now().UTC()
	payload, err := DecodeActivatePayload(validActivation(now), testNodeID, now)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "device-sessions")
	if err := Activate(root, payload); err != nil {
		t.Fatal(err)
	}
	if err := Activate(root, payload); err != nil {
		t.Fatalf("exact activation retry was not idempotent: %v", err)
	}
	changedBinding := payload
	changedBinding.DeviceID = "723e4567-e89b-42d3-a456-426614174006"
	if err := Activate(root, changedBinding); err == nil {
		t.Fatal("expected same-generation binding change rejection")
	}
	path := filepath.Join(root, testDeviceSessionID+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected owner-only activation, got %o", info.Mode().Perm())
	}

	newer := payload
	newer.Generation = 3
	if err := Activate(root, newer); err != nil {
		t.Fatal(err)
	}
	if err := Activate(root, payload); err == nil {
		t.Fatal("expected stale activation rejection")
	}
	removed, err := Deactivate(root, DeactivatePayload{
		DeviceSessionID: testDeviceSessionID, ToolSessionID: testToolSessionID, Generation: 2,
	})
	if err != nil || removed {
		t.Fatalf("stale deactivation changed state: removed=%v err=%v", removed, err)
	}
	removed, err = Deactivate(root, DeactivatePayload{
		DeviceSessionID: testDeviceSessionID, ToolSessionID: testToolSessionID, Generation: 4,
	})
	if err != nil || !removed {
		t.Fatalf("expected deactivation: removed=%v err=%v", removed, err)
	}
}

// TestActivationRejectsUnknownFieldsBindingsAndUnsafeRoot verifies untrusted activation input rejection.
func TestActivationRejectsUnknownFieldsBindingsAndUnsafeRoot(t *testing.T) {
	now := time.Now().UTC()
	unknown := validActivation(now)
	unknown["relay_ticket"] = "must-not-be-accepted"
	if _, err := DecodeActivatePayload(unknown, testNodeID, now); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if _, err := DecodeActivatePayload(validActivation(now), "623e4567-e89b-42d3-a456-426614174005", now); err == nil {
		t.Fatal("expected node binding rejection")
	}

	temporary := t.TempDir()
	realRoot := filepath.Join(temporary, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkRoot := filepath.Join(temporary, "linked")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	payload, err := DecodeActivatePayload(validActivation(now), testNodeID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := Activate(symlinkRoot, payload); err == nil {
		t.Fatal("expected symlink root rejection")
	}
	if _, err := Deactivate(symlinkRoot, DeactivatePayload{
		DeviceSessionID: testDeviceSessionID, ToolSessionID: testToolSessionID, Generation: 2,
	}); err == nil {
		t.Fatal("expected deactivation through symlink root rejection")
	}
}

// TestActivationRejectsUnsafeManifestFiles verifies descriptor-bound manifest safety checks.
func TestActivationRejectsUnsafeManifestFiles(t *testing.T) {
	now := time.Now().UTC()
	payload, err := DecodeActivatePayload(validActivation(now), testNodeID, now)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "device-sessions")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := manifestPath(root, payload.DeviceSessionID)
	target := filepath.Join(root, "target.json")
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(path); err == nil {
		t.Fatal("expected symlink manifest rejection")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maximumActivationManifestSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(path); err == nil {
		t.Fatal("expected oversized manifest rejection")
	}
}

// TestActivationRejectsNoncanonicalManifestJSON verifies strict persisted-state decoding.
func TestActivationRejectsNoncanonicalManifestJSON(t *testing.T) {
	root := t.TempDir()
	path := manifestPath(root, testDeviceSessionID)
	for name, data := range map[string][]byte{
		"duplicate key": []byte(`{"generation":1,"generation":2}`),
		"unknown field": []byte(`{"unexpected":true}`),
		"trailing JSON": []byte(`{} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadManifest(path); err == nil {
				t.Fatalf("expected %s manifest rejection", name)
			}
		})
	}
}

// TestContextPayloadRejectsUnknownFieldsWrongNodeAndLongLease verifies strict context validation.
func TestContextPayloadRejectsUnknownFieldsWrongNodeAndLongLease(t *testing.T) {
	now := time.Now().UTC()
	payload := validActivation(now)
	delete(payload, "expires_at")
	delete(payload, "runtime_backend")
	delete(payload, "runtime_resource_id")
	payload["lease_until"] = now.Add(time.Minute).Format(time.RFC3339Nano)
	if _, err := DecodeContextPayload(payload, testNodeID, now); err != nil {
		t.Fatal(err)
	}
	payload["capabilities"] = SupportedV2Capabilities()
	if _, err := DecodeContextPayload(payload, testNodeID, now); err != nil {
		t.Fatalf("complete v2 capabilities were rejected: %v", err)
	}
	payload["capabilities"] = []string{"ax_state_v2"}
	if _, err := DecodeContextPayload(payload, testNodeID, now); err == nil {
		t.Fatal("expected partial v2 capability rejection")
	}
	payload["capabilities"] = SupportedV2Capabilities()
	payload["relay_ticket"] = "forbidden"
	if _, err := DecodeContextPayload(payload, testNodeID, now); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	delete(payload, "relay_ticket")
	if _, err := DecodeContextPayload(payload, "623e4567-e89b-42d3-a456-426614174005", now); err == nil {
		t.Fatal("expected node binding rejection")
	}
	payload["lease_until"] = now.Add(10 * time.Minute).Format(time.RFC3339Nano)
	if _, err := DecodeContextPayload(payload, testNodeID, now); err == nil {
		t.Fatal("expected long lease rejection")
	}
}

// TestPayloadDecodersEnforceSharedGenerationBounds verifies the signed database boundary at Node ingress.
func TestPayloadDecodersEnforceSharedGenerationBounds(t *testing.T) {
	now := time.Now().UTC()
	activation := validActivation(now)
	activation["generation"] = MaximumActiveDeviceSessionGeneration
	if _, err := DecodeActivatePayload(activation, testNodeID, now); err != nil {
		t.Fatalf("maximum active generation was rejected: %v", err)
	}
	activation["generation"] = MaximumDeviceSessionGeneration
	if _, err := DecodeActivatePayload(activation, testNodeID, now); err == nil {
		t.Fatal("expected terminal-only generation activation rejection")
	}

	context := validActivation(now)
	delete(context, "expires_at")
	delete(context, "runtime_backend")
	delete(context, "runtime_resource_id")
	context["lease_until"] = now.Add(time.Minute).Format(time.RFC3339Nano)
	context["generation"] = MaximumDeviceSessionGeneration
	if _, err := DecodeContextPayload(context, testNodeID, now); err == nil {
		t.Fatal("expected terminal-only context generation rejection")
	}

	deactivation := map[string]any{
		"device_session_id": testDeviceSessionID,
		"tool_session_id":   testToolSessionID,
		"generation":        MaximumDeviceSessionGeneration,
	}
	if _, err := DecodeDeactivatePayload(deactivation); err != nil {
		t.Fatalf("maximum terminal generation was rejected: %v", err)
	}
	deactivation["generation"] = uint64(1 << 63)
	if _, err := DecodeDeactivatePayload(deactivation); err == nil {
		t.Fatal("expected out-of-range deactivation rejection")
	}
}
