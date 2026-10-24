package claudeattachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func transferIdentityFixture() TransferIdentity {
	return TransferIdentity{
		NodeID:           "11111111-1111-4111-8111-111111111111",
		SessionID:        "22222222-2222-4222-8222-222222222222",
		ExpectedDeviceID: "33333333-3333-4333-8333-333333333333",
		SSHKeyID:         "44444444-4444-4444-8444-444444444444",
		LeaseID:          "55555555-5555-4555-8555-555555555555",
	}
}

func TestExactAttachmentGatewayStagesAndCleansLease(t *testing.T) {
	identity := transferIdentityFixture()
	root := t.TempDir()
	account := filepath.Join(root, "user", "tool-accounts", "claude", "account")
	sessionRoot := filepath.Join(account, ".agent-remote-attachments", identity.SessionID)
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte("hello attachment")
	frame := func(value any) []byte {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return append(encoded, '\n')
	}
	input := bytes.Buffer{}
	input.Write(frame(OpenFrame{Version: 1, Operation: "open", SessionID: identity.SessionID, ExpectedDeviceID: identity.ExpectedDeviceID, LeaseID: identity.LeaseID}))
	digest := sha256.Sum256(data)
	input.Write(frame(UploadFrame{Version: 1, Operation: "upload", SessionID: identity.SessionID, ExpectedDeviceID: identity.ExpectedDeviceID, LeaseID: identity.LeaseID, BatchID: "66666666-6666-4666-8666-666666666666", Kind: "file", Name: "hello.txt", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}))
	input.Write(data)
	input.Write(frame(CleanupFrame{Version: 1, Operation: "cleanup", SessionID: identity.SessionID, ExpectedDeviceID: identity.ExpectedDeviceID, LeaseID: identity.LeaseID}))
	var output bytes.Buffer
	gateway := Gateway{Identity: identity, AccountPath: account, LeaseExpiresAt: time.Now().Add(time.Minute), MaxBytes: 1 << 20, MaxEntries: 2, ReceiptRoot: filepath.Join(root, "receipts")}
	if err := gateway.Serve(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(account, ".agent-remote-attachments", identity.SessionID, identity.LeaseID)); !os.IsNotExist(err) {
		t.Fatalf("lease directory survived cleanup: %v", err)
	}
	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatal("invalid gateway response", err)
		}
		frames = append(frames, value)
	}
	if len(frames) != 4 || frames[0]["operation"] != "lease_offer" || frames[1]["operation"] != "open_ack" || frames[2]["operation"] != "upload_ack" || frames[3]["operation"] != "cleanup_receipt" {
		t.Fatalf("unexpected exact gateway response sequence: %#v", frames)
	}
	path, ok := frames[2]["path"].(string)
	if !ok || !strings.Contains(path, filepath.Join(".agent-remote-attachments", identity.SessionID, identity.LeaseID)) {
		t.Fatalf("upload acknowledgement did not return a derived session path: %#v", frames[2])
	}
}

func TestExactAttachmentFramesRejectDuplicateAndTraversal(t *testing.T) {
	identity := transferIdentityFixture()
	if err := (UploadFrame{Version: 1, Operation: "upload", SessionID: identity.SessionID, ExpectedDeviceID: identity.ExpectedDeviceID, LeaseID: identity.LeaseID, BatchID: identity.LeaseID, Kind: "file", Name: "../escape", Size: 1, SHA256: strings.Repeat("a", 64)}).Validate(identity, 10, 1); err == nil {
		t.Fatal("path traversal accepted")
	}
	var frame OpenFrame
	if err := decodeStrict([]byte(`{"version":1,"operation":"open","session_id":"22222222-2222-4222-8222-222222222222","session_id":"22222222-2222-4222-8222-222222222222","expected_device_id":"33333333-3333-4333-8333-333333333333","lease_id":"55555555-5555-4555-8555-555555555555"}`), &frame); err == nil {
		t.Fatal("duplicate frame field accepted")
	}
}

func TestResolveSessionAccountRejectsAmbiguousMapping(t *testing.T) {
	identity := transferIdentityFixture()
	root := t.TempDir()
	for _, account := range []string{"one", "two"} {
		path := filepath.Join(root, "user", "tool-accounts", "claude", account, ".agent-remote-attachments", identity.SessionID)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResolveSessionAccount(root, identity.SessionID); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("ambiguous session mapping accepted: %v", err)
	}
}
