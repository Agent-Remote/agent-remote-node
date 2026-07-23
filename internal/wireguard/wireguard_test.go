package wireguard

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(seed byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string([]byte{seed}), 32)))
}

func TestDecodeSyncPayloadAndRender(t *testing.T) {
	privateKey := testKey(1)
	publicKey := testKey(2)
	payload, err := DecodeSyncPayload(map[string]any{
		"peers": []any{map[string]any{
			"public_key":  publicKey,
			"allowed_ips": []any{"10.77.0.2/32"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderSyncConfig(privateKey, 51820, payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"PrivateKey = " + privateKey,
		"ListenPort = 51820",
		"PublicKey = " + publicKey,
		"AllowedIPs = 10.77.0.2/32",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("rendered config is missing %q: %s", expected, rendered)
		}
	}
}

func TestDecodeSyncPayloadRejectsUnsafeValues(t *testing.T) {
	key := testKey(3)
	invalid := []map[string]any{
		{"peers": []any{map[string]any{"public_key": "bad", "allowed_ips": []any{"10.77.0.2/32"}}}},
		{"peers": []any{map[string]any{"public_key": key, "allowed_ips": []any{"10.77.0.0/24"}}}},
		{"peers": []any{
			map[string]any{"public_key": key, "allowed_ips": []any{"10.77.0.2/32"}},
			map[string]any{"public_key": testKey(4), "allowed_ips": []any{"10.77.0.2/32"}},
		}},
	}
	for _, payload := range invalid {
		if _, err := DecodeSyncPayload(payload); err == nil {
			t.Fatalf("expected payload to be rejected: %#v", payload)
		}
	}
}

func TestValidateInterface(t *testing.T) {
	if err := ValidateInterface("agent-remote"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "too-long-interface", "wg0;reboot"} {
		if err := ValidateInterface(value); err == nil {
			t.Fatalf("expected interface %q to be rejected", value)
		}
	}
}
