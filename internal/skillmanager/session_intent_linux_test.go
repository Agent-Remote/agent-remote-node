package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func specIntentFixture() SessionSpecIntent {
	fence := accountFenceFixture()
	return SessionSpecIntent{Version: 1, Identity: SkillSnapshotIdentity{
		NodeID: fence.NodeID, UserID: fence.UserID, AccountID: fence.AccountID,
		SessionID: "44444444-4444-4444-8444-444444444444", SnapshotID: "55555555-5555-4555-8555-555555555555",
		TaskID: "66666666-6666-4666-8666-666666666666", RuntimeBackend: "native",
	}, InputDigest: strings.Repeat("a", 64), State: "started"}
}

func TestSessionSpecIntentDraftAndImmutableReceipt(t *testing.T) {
	root, path := privateStore(t)
	intent := specIntentFixture()
	data := []byte(`{"version":1,"argv":["original"]}`)
	digest := sha256.Sum256(data)
	hexDigest := hex.EncodeToString(digest[:])
	if err := PublishSessionSpecDraft(root, intent, data); err == nil {
		t.Fatal("draft accepted without prior intent")
	}
	if err := BeginSessionSpecIntent(root, intent); err != nil {
		t.Fatal(err)
	}
	if err := FinishSessionSpecIntent(root, intent, hexDigest); err == nil {
		t.Fatal("ready without draft")
	}
	if err := PublishSessionSpecDraft(root, intent, data); err != nil {
		t.Fatal(err)
	}
	if err := PublishSessionSpecDraft(root, intent, []byte(`{"version":2}`)); err == nil {
		t.Fatal("replaced draft")
	}
	if err := FinishSessionSpecIntent(root, intent, strings.Repeat("b", 64)); err == nil {
		t.Fatal("sealed different spec")
	}
	if err := FinishSessionSpecIntent(root, intent, hexDigest); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := FinishSessionSpecIntent(reopened, intent, hexDigest); err != nil {
		t.Fatal(err)
	}
	ready, err := ReadSessionSpecIntent(reopened, intent)
	if err != nil || ready.State != "ready" || ready.SpecDigest != hexDigest {
		t.Fatal("lost ready receipt", err)
	}
	for _, change := range []func(*SessionSpecIntent){
		func(i *SessionSpecIntent) { i.InputDigest = strings.Repeat("c", 64) },
		func(i *SessionSpecIntent) { i.Identity.TaskID = i.Identity.NodeID },
		func(i *SessionSpecIntent) { i.Identity.UserID = i.Identity.AccountID },
	} {
		other := intent
		change(&other)
		if _, err := ReadSessionSpecDraft(reopened, other); err == nil {
			t.Fatal("accepted changed input")
		}
	}
	if err := reopened.Remove("spec-draft-" + intent.Identity.SessionID + ".json"); err != nil {
		t.Fatal(err)
	}
	if err := PublishSessionSpecDraft(reopened, intent, data); err == nil {
		t.Fatal("repaired missing ready draft")
	}
}

func TestSessionSpecPrivateRecordsRejectCorruption(t *testing.T) {
	for _, prefix := range []string{"spec-intent-", "spec-draft-"} {
		for _, kind := range []string{"json", "mode", "symlink", "hardlink", "bytes"} {
			t.Run(prefix+kind, func(t *testing.T) {
				root, path := privateStore(t)
				intent := specIntentFixture()
				if err := BeginSessionSpecIntent(root, intent); err != nil {
					t.Fatal(err)
				}
				if err := PublishSessionSpecDraft(root, intent, []byte(`{"version":1}`)); err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(path, prefix+intent.Identity.SessionID+".json")
				var err error
				switch kind {
				case "json":
					err = os.WriteFile(name, []byte("{"), 0o600)
				case "mode":
					err = os.Chmod(name, 0o644)
				case "symlink":
					if err = os.Rename(name, name+".old"); err == nil {
						err = os.Symlink(name+".old", name)
					}
				case "hardlink":
					err = os.Link(name, name+".link")
				case "bytes":
					var data []byte
					data, err = os.ReadFile(name)
					if err == nil {
						err = os.WriteFile(name, []byte(strings.Replace(string(data), `"version":1`, `"version":2`, 1)), 0o600)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ReadSessionSpecDraft(root, intent); err == nil || errors.Is(err, os.ErrNotExist) {
					t.Fatal("unsafe record accepted or treated as missing", err)
				}
			})
		}
	}
}
