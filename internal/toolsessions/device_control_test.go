package toolsessions

import "testing"

// TestDecodeCreatePayloadAcceptsOnlyNativeClaudeDeviceControl verifies strict tool-session decoding.
func TestDecodeCreatePayloadAcceptsOnlyNativeClaudeDeviceControl(t *testing.T) {
	payload := map[string]any{
		"session_id": "session_1", "tool_account_id": "account_1", "tool_type": "claude",
		"user_id": "user_1", "workspace_id": "workspace_1", "tmux_session_name": "tmux_1",
		"sandbox_name": "sandbox_1", "runtime_backend": "native",
		"device_control": map[string]any{"protocol_version": float64(1)},
	}
	decoded, err := DecodeCreatePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.DeviceControl == nil || decoded.DeviceControl.ProtocolVersion != 1 {
		t.Fatalf("unexpected device-control configuration: %#v", decoded.DeviceControl)
	}

	for _, change := range []func(map[string]any){
		func(value map[string]any) { value["runtime_backend"] = "docker_sandbox" },
		func(value map[string]any) { value["tool_type"] = "future_tool" },
		func(value map[string]any) { value["device_control"] = map[string]any{"protocol_version": float64(2)} },
	} {
		candidate := make(map[string]any, len(payload))
		for key, value := range payload {
			candidate[key] = value
		}
		change(candidate)
		if _, err := DecodeCreatePayload(candidate); err == nil {
			t.Fatalf("expected invalid configuration rejection: %#v", candidate)
		}
	}
}
