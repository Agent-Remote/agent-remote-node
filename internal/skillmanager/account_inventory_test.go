package skillmanager

import "testing"

func TestAccountInventoryMatchesServerCanonicalDigest(t *testing.T) {
	// Python json.dumps([], sort_keys=True, separators=(",", ":"), ensure_ascii=True).
	actual, err := AccountInventoryDigest(nil)
	if err != nil || actual != "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945" {
		t.Fatalf("empty inventory hash mismatch: %s %v", actual, err)
	}
	writer := AccountWriter{Kind: "binding", NodeID: "33333333-3333-4333-8333-333333333333", ResourceID: "44444444-4444-4444-8444-444444444444"}
	actual, err = AccountInventoryDigest([]AccountWriter{writer})
	if err != nil || actual != "b6c31f60c2c661dc238c30cb0423ac1553dc5036603f1072dac752cb98b44492" {
		t.Fatalf("nullable inventory hash mismatch: %s %v", actual, err)
	}
	backend := "native"
	writer.RuntimeBackend = &backend
	if changed, err := AccountInventoryDigest([]AccountWriter{writer}); err != nil || changed == actual {
		t.Fatal("known backend did not change inventory identity", err)
	}
}

func TestAccountInventoryRejectsUnverifiableResources(t *testing.T) {
	for _, writer := range []AccountWriter{
		{Kind: "session", NodeID: "bad", ResourceID: "x"},
		{Kind: "import", NodeID: "33333333-3333-4333-8333-333333333333", ResourceID: "import:x"},
		{Kind: "session", NodeID: "33333333-3333-4333-8333-333333333333", ResourceID: "/host/path"},
	} {
		if _, err := AccountInventoryDigest([]AccountWriter{writer}); err == nil {
			t.Fatal("invalid inventory accepted")
		}
	}
	if _, err := AccountInventoryDigest(make([]AccountWriter, 10_001)); err == nil {
		t.Fatal("unbounded inventory accepted")
	}
}
