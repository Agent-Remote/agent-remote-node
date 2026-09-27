package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestContentVerifierHandlesSplitUTF8AndBinaryWithoutBuffering(t *testing.T) {
	for _, data := range [][]byte{[]byte("多字节🎉"), {}, {0}, {0xff}, {0xe4, 0xbd}} {
		for _, kind := range []string{"text", "binary"} {
			digest := sha256.Sum256(data)
			entry := Entry{Path: "state", Kind: "file", Mode: 0o600, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), ContentKind: kind}
			expected := VerifyContent(entry, data)
			for split := 0; split <= len(data); split++ {
				verifier, err := NewContentVerifier(entry)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = verifier.Write(data[:split])
				_, _ = verifier.Write(data[split:])
				if (verifier.Finish() == nil) != (expected == nil) {
					t.Fatalf("split %d classification %s disagrees", split, kind)
				}
			}
		}
	}
}

func TestContentVerifierRejectsMissingExcessAndWrongBytes(t *testing.T) {
	digest := sha256.Sum256([]byte("correct"))
	entry := Entry{Path: "state", Kind: "file", Mode: 0o600, Size: 7, SHA256: hex.EncodeToString(digest[:]), ContentKind: "text"}
	for _, data := range []string{"", "correct-extra", "changed"} {
		verifier, err := NewContentVerifier(entry)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = verifier.Write([]byte(data))
		if verifier.Finish() == nil {
			t.Fatal("invalid content accepted")
		}
	}
	if _, err := NewContentVerifier(Entry{Path: "dir", Kind: "directory", Mode: 0o700}); err == nil {
		t.Fatal("non-file accepted")
	}
}
