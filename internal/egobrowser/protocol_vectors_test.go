package egobrowser

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type protocolVectorDocument struct {
	SchemaVersion int `json:"schema_version"`
	CanonicalJSON []struct {
		Input     map[string]any `json:"input"`
		Canonical string         `json:"canonical"`
	} `json:"canonical_json"`
	DevicePoPV2      deviceProofVector `json:"device_pop_v2"`
	ValidOuter       json.RawMessage   `json:"valid_outer"`
	ValidCancelOuter json.RawMessage   `json:"valid_cancel_outer"`
	InvalidOuterJSON []string          `json:"invalid_outer_json"`
	InvalidOuter     []json.RawMessage `json:"invalid_outer"`
	Capability       json.RawMessage   `json:"capability"`
	ValidInner       json.RawMessage   `json:"valid_inner"`
	ValidCancelInner json.RawMessage   `json:"valid_cancel_inner"`
}

type deviceProofVector struct {
	Operation           string                   `json:"operation"`
	DeviceID            string                   `json:"device_id"`
	DeviceGeneration    uint64                   `json:"device_generation"`
	OperationGeneration uint64                   `json:"operation_generation"`
	BindingID           string                   `json:"binding_id"`
	ReleaseProfile      string                   `json:"release_profile"`
	CredentialProfile   string                   `json:"credential_profile"`
	ServerHost          string                   `json:"server_host"`
	Challenge           string                   `json:"challenge"`
	Payload             deviceProofVectorPayload `json:"payload"`
	CanonicalPayload    string                   `json:"canonical_payload"`
	MessageSHA256       string                   `json:"message_sha256"`
	PublicKey           string                   `json:"public_key"`
	Signature           string                   `json:"signature"`
}

type deviceProofVectorPayload struct {
	AllowlistRevision       uint64  `json:"allowlist_revision"`
	Generation              uint64  `json:"generation"`
	LearningBundleDigest    *string `json:"learning_bundle_digest"`
	SignerCertificateSHA256 string  `json:"signer_certificate_sha256"`
}

func appendProofField(message *bytes.Buffer, value string) {
	_ = binary.Write(message, binary.BigEndian, uint32(len(value)))
	message.WriteString(value)
}

func verifyDeviceProofVector(t *testing.T, vector deviceProofVector) {
	t.Helper()
	payload, err := json.Marshal(vector.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != vector.CanonicalPayload {
		t.Fatalf("canonical proof payload = %s, want %s", payload, vector.CanonicalPayload)
	}
	challenge, err := base64.RawURLEncoding.DecodeString(vector.Challenge)
	if err != nil || len(challenge) != 32 {
		t.Fatalf("decode proof challenge: %v", err)
	}
	message := bytes.NewBufferString("agent-remote/ego-browser/pop/v2\x00")
	appendProofField(message, vector.Operation)
	appendProofField(message, vector.DeviceID)
	_ = binary.Write(message, binary.BigEndian, vector.DeviceGeneration)
	_ = binary.Write(message, binary.BigEndian, vector.OperationGeneration)
	for _, value := range []string{vector.BindingID, vector.ReleaseProfile, vector.CredentialProfile, vector.ServerHost} {
		appendProofField(message, value)
	}
	message.Write(challenge)
	payloadDigest := sha256.Sum256(payload)
	message.Write(payloadDigest[:])
	messageDigest := sha256.Sum256(message.Bytes())
	if hex.EncodeToString(messageDigest[:]) != vector.MessageSHA256 {
		t.Fatal("shared device proof transcript digest does not match")
	}
	publicKey, keyErr := base64.RawURLEncoding.DecodeString(vector.PublicKey)
	signature, signatureErr := base64.RawURLEncoding.DecodeString(vector.Signature)
	if keyErr != nil || signatureErr != nil ||
		len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(publicKey, message.Bytes(), signature) {
		t.Fatal("shared device proof signature does not verify")
	}
}

func TestSharedProtocolVectors(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve protocol vector test path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "agent-remote-ego-browser", "protocol", "test-vectors", "ego-browser-bridge-v1.json"))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("authoritative ego-browser protocol vectors are not checked out")
	}
	if err != nil {
		t.Fatal(err)
	}
	var vectors protocolVectorDocument
	if err := decodeStrictJSON(data, &vectors); err != nil {
		t.Fatalf("decode shared vectors: %v", err)
	}
	if vectors.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", vectors.SchemaVersion)
	}
	for _, vector := range vectors.CanonicalJSON {
		encoded, err := json.Marshal(vector.Input)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != vector.Canonical {
			t.Fatalf("canonical JSON = %s, want %s", encoded, vector.Canonical)
		}
	}
	verifyDeviceProofVector(t, vectors.DevicePoPV2)

	var outer outerEnvelope
	if err := decodeStrictJSON(vectors.ValidOuter, &outer); err != nil {
		t.Fatalf("decode valid outer: %v", err)
	}
	if err := validateWrappedOuter(outer, len(vectors.ValidOuter)); err != nil {
		t.Fatalf("validate shared outer: %v", err)
	}
	wrapperOuter := outer
	wrapperOuter.KeyWrap = ""
	if err := validateOuter(wrapperOuter, len(vectors.ValidOuter)); err != nil {
		t.Fatalf("validate shared wrapper metadata: %v", err)
	}
	var cancelOuter outerEnvelope
	if err := decodeStrictJSON(vectors.ValidCancelOuter, &cancelOuter); err != nil {
		t.Fatalf("decode valid cancel outer: %v", err)
	}
	if err := validateCancelOuter(cancelOuter, len(vectors.ValidCancelOuter)); err != nil {
		t.Fatalf("validate shared cancel outer: %v", err)
	}
	var cancelInner innerCancelRequest
	if err := decodeStrictJSON(vectors.ValidCancelInner, &cancelInner); err != nil {
		t.Fatalf("decode valid cancel inner: %v", err)
	}
	if cancelInner.Protocol != InnerProtocolVersion || cancelInner.Type != "cancel" ||
		cancelInner.RequestID != cancelOuter.RequestID || cancelInner.Sequence != cancelOuter.Sequence {
		t.Fatalf("shared cancel identity is incompatible: %#v", cancelInner)
	}
	for _, raw := range vectors.InvalidOuterJSON {
		if err := decodeStrictJSON([]byte(raw), &outer); err == nil {
			t.Fatalf("accepted invalid outer JSON: %s", raw)
		}
	}
	for _, raw := range vectors.InvalidOuter {
		if err := decodeStrictJSON(raw, &outer); err != nil {
			t.Fatalf("invalid metadata vector must remain structurally typed: %v", err)
		}
		wrapperOuter = outer
		wrapperOuter.KeyWrap = ""
		if err := validateOuter(wrapperOuter, len(raw)); err == nil {
			t.Fatal("accepted invalid outer metadata vector")
		}
	}

	var capability bridgeCapabilityWire
	if err := decodeStrictJSON(vectors.Capability, &capability); err != nil {
		t.Fatalf("decode capability vector: %v", err)
	}
	if capability.BridgeProtocolVersion != ProtocolVersion || capability.RemotePlatform != "linux" || capability.LocalPlatform != "macos" || capability.SkillVersion != "1.2.3" || capability.LocalRuntimeVersion != "0.4.7.4" {
		t.Fatalf("shared capability identity is incompatible: %#v", capability)
	}
}
