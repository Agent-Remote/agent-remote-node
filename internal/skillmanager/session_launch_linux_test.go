package skillmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func launchFixture(t *testing.T, root *os.Root) SessionLaunch {
	t.Helper()
	spec := specIntentFixture()
	data := []byte(`{"test":"original-spec"}`)
	digest := sha256.Sum256(data)
	if err := BeginSessionSpecIntent(root, spec); err != nil {
		t.Fatal(err)
	}
	if err := PublishSessionSpecDraft(root, spec, data); err != nil {
		t.Fatal(err)
	}
	if err := FinishSessionSpecIntent(root, spec, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	spec.State, spec.SpecDigest = "ready", hex.EncodeToString(digest[:])
	return SessionLaunch{Version: 1, Spec: spec, BootID: "77777777-7777-4777-8777-777777777777", UnitName: "agent-remote-session-123456789abc.service", State: "starting"}
}

func TestSessionLaunchRequiresOriginalReadySpecAndCannotReplaceInvocation(t *testing.T) {
	root, path := privateStore(t)
	launch := launchFixture(t, root)
	if err := BeginSessionLaunch(root, launch); err != nil {
		t.Fatal(err)
	}
	if err := BeginSessionLaunch(root, launch); err == nil {
		t.Fatal("launch intent replaced")
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := ReadSessionLaunch(reopened, launch)
	if err != nil || saved != launch {
		t.Fatal("lost starting intent", err)
	}
	if err := FinishSessionLaunch(root, launch, strings.Repeat("0", 32)); err == nil {
		t.Fatal("empty invocation accepted")
	}
	invocation := strings.Repeat("a", 32)
	if err := FinishSessionLaunch(root, launch, invocation); err != nil {
		t.Fatal(err)
	}
	if err := FinishSessionLaunch(root, launch, invocation); err != nil {
		t.Fatal("exact replay failed", err)
	}
	if err := FinishSessionLaunch(root, launch, strings.Repeat("b", 32)); err == nil {
		t.Fatal("changed original invocation")
	}
	for _, change := range []func(*SessionLaunch){
		func(v *SessionLaunch) { v.BootID = v.Spec.Identity.NodeID },
		func(v *SessionLaunch) { v.Spec.InputDigest = strings.Repeat("c", 64) },
		func(v *SessionLaunch) { v.Spec.Identity.TaskID = v.Spec.Identity.NodeID },
		func(v *SessionLaunch) { v.UnitName = "agent-remote-session-aaaaaaaaaaaa.service" },
	} {
		changed := launch
		change(&changed)
		if _, err := ReadSessionLaunch(root, changed); err == nil {
			t.Fatal("changed launch binding accepted")
		}
	}
	if err := root.Remove("spec-draft-" + launch.Spec.Identity.SessionID + ".json"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSessionLaunch(root, launch); err == nil {
		t.Fatal("receipt lost original authority")
	}
}

func TestSessionLaunchObservationPinsInvocationWithoutReadiness(t *testing.T) {
	root, path := privateStore(t)
	launch := launchFixture(t, root)
	if err := BeginSessionLaunch(root, launch); err != nil {
		t.Fatal(err)
	}
	invocation := strings.Repeat("a", 32)
	for _, invalid := range []string{"", strings.Repeat("0", 32), strings.Repeat("A", 32), "short"} {
		if err := ObserveSessionLaunch(root, launch, invalid); err == nil {
			t.Fatal("invalid observation accepted")
		}
	}
	if err := ObserveSessionLaunch(root, launch, invocation); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	observed, err := ReadSessionLaunch(reopened, launch)
	if err != nil || observed.State != "observed" || observed.InvocationID != invocation {
		t.Fatal("observation lost or promoted to readiness", err)
	}
	for range 2 {
		if err := ObserveSessionLaunch(reopened, launch, invocation); err != nil {
			t.Fatal("observation replay failed", err)
		}
	}
	if err := BeginSessionLaunch(reopened, launch); err == nil {
		t.Fatal("observed launch restarted")
	}
	replacement := strings.Repeat("b", 32)
	if err := ObserveSessionLaunch(reopened, launch, replacement); err == nil {
		t.Fatal("observation changed invocation")
	}
	if err := FinishSessionLaunch(reopened, launch, replacement); err == nil {
		t.Fatal("readiness replaced observed invocation")
	}
	if err := FinishSessionLaunch(reopened, launch, invocation); err != nil {
		t.Fatal(err)
	}
	if err := ObserveSessionLaunch(reopened, launch, invocation); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadSessionLaunch(reopened, launch)
	if err != nil || saved.State != "started" || saved.InvocationID != invocation {
		t.Fatal("observation downgraded readiness", err)
	}
}
