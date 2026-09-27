package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

type snapshotPreparationFixture struct {
	t             *testing.T
	snapshot      skillmanager.SkillSnapshot
	session       toolsessions.CreatePayload
	calls         []string
	renewals      atomic.Int32
	denyInitial   bool
	loseLease     bool
	blockRenewal  bool
	wrongSnapshot bool
	failSpec      bool
	failContent   bool
}

func newSnapshotPreparationFixture(t *testing.T) *snapshotPreparationFixture {
	t.Helper()
	binding := skillmanager.SkillSnapshotIdentity{UserID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222", NodeID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444", SnapshotID: "55555555-5555-4555-8555-555555555555", TaskID: "66666666-6666-4666-8666-666666666666", RuntimeBackend: "native"}
	hash := sha256.Sum256([]byte("original"))
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{{Path: "notes", Kind: "file", Mode: 0o644, Size: 8, SHA256: hex.EncodeToString(hash[:]), ContentKind: "text"}}}
	digest, err := skillmanager.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	pins, err := json.Marshal(skillmanager.EgoBrowserRelease{Version: egobrowserartifact.OfficialSkillVersion, Commit: egobrowserartifact.OfficialSkillSourceCommit, TreeSHA256: egobrowserartifact.OfficialSkillTreeSHA256})
	if err != nil {
		t.Fatal(err)
	}
	return &snapshotPreparationFixture{t: t,
		snapshot: skillmanager.SkillSnapshot{SkillSnapshotIdentity: binding, LibraryGeneration: 9007199254740993, DirectoryEpoch: 9007199254740995, TreeDigest: digest, Manifest: manifest, Items: []skillmanager.SkillSnapshotItem{}, SystemReleases: map[string]json.RawMessage{"ego-browser": pins}},
		session:  toolsessions.CreatePayload{UserID: binding.UserID, ToolAccountID: binding.AccountID, SessionID: binding.SessionID, RuntimeBackend: "native", ToolType: "claude", WorkspaceID: binding.TaskID},
	}
}

func (f *snapshotPreparationFixture) RenewSkillSnapshotLease(ctx context.Context, binding skillmanager.SkillSnapshotIdentity, attempt int64) (api.SkillSnapshotLease, error) {
	if binding != f.snapshot.SkillSnapshotIdentity || attempt != 3 {
		f.t.Error("renewal changed original authority")
	}
	count := f.renewals.Add(1)
	if f.denyInitial || count > 1 && f.loseLease {
		if f.blockRenewal {
			<-ctx.Done()
		}
		return api.SkillSnapshotLease{}, errors.New("private lease denial")
	}
	server := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	return api.SkillSnapshotLease{SkillSnapshotIdentity: binding, LeaseAttempt: attempt, ServerTime: server, LeaseUntil: server.Add(200 * time.Millisecond), RenewAfterMilliseconds: 20}, nil
}

func (f *snapshotPreparationFixture) GetSkillSnapshot(_ context.Context, binding skillmanager.SkillSnapshotIdentity) (api.SkillSnapshot, error) {
	f.calls = append(f.calls, "snapshot")
	if binding != f.snapshot.SkillSnapshotIdentity {
		f.t.Error("download changed original authority")
	}
	snapshot := f.snapshot
	if f.wrongSnapshot {
		snapshot.TaskID = snapshot.NodeID
	}
	return snapshot, nil
}

func (f *snapshotPreparationFixture) ReadSkillSnapshotFile(_ context.Context, binding skillmanager.SkillSnapshotIdentity, entry skillmanager.Entry, target io.Writer) error {
	f.calls = append(f.calls, "file")
	if binding != f.snapshot.SkillSnapshotIdentity || entry != f.snapshot.Manifest.Entries[0] {
		f.t.Error("file download escaped original manifest")
	}
	if f.failContent {
		return errors.New("private download failure")
	}
	_, err := io.WriteString(target, "original")
	return err
}

func (f *snapshotPreparationFixture) PrepareManagedSessionSpec(_ context.Context, task string, request runtimehelper.ManagedSessionSpecRequest) (map[string]any, error) {
	f.calls = append(f.calls, "spec")
	digest, _ := f.snapshot.InputDigest()
	if task != "original-task" || request.Snapshot != f.snapshot.SkillSnapshotIdentity || request.SnapshotInputDigest != digest || !reflect.DeepEqual(request.Session, f.session) {
		f.t.Error("spec input changed")
	}
	if f.failSpec {
		return nil, errors.New("private spec failure")
	}
	return map[string]any{"runtime_uid": 12345}, nil
}

func (f *snapshotPreparationFixture) PrepareSkillSnapshot(ctx context.Context, task string, snapshot skillmanager.SkillSnapshot, download runtimehelper.SkillSnapshotDownloader) error {
	f.calls = append(f.calls, "prepare")
	if task != "original-task" || !reflect.DeepEqual(snapshot, f.snapshot) {
		f.t.Error("stream changed original snapshot")
	}
	if f.loseLease {
		<-ctx.Done()
		return ctx.Err()
	}
	var target strings.Builder
	if err := download(ctx, snapshot.Manifest.Entries[0], &target); err != nil {
		return err
	}
	if target.String() != "original" {
		f.t.Error("stream lost original bytes")
	}
	return nil
}

func TestManagedSnapshotPreparationOrdersAuthorizedOperations(t *testing.T) {
	f := newSnapshotPreparationFixture(t)
	receipt, err := prepareManagedSnapshot(context.Background(), f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := f.snapshot.InputDigest()
	if receipt.Identity != f.snapshot.SkillSnapshotIdentity || receipt.InputDigest != digest || receipt.RuntimeUID != 12345 {
		t.Fatal("incomplete preparation receipt")
	}
	if !reflect.DeepEqual(f.calls, []string{"snapshot", "spec", "prepare", "file"}) || f.renewals.Load() < 1 {
		t.Fatal("preparation escaped authorization order", f.calls)
	}
	before := f.renewals.Load()
	time.Sleep(40 * time.Millisecond)
	if f.renewals.Load() != before {
		t.Fatal("lease loop outlived preparation")
	}
}

func TestManagedSnapshotPreparationFailurePreservesNoSuccessReceipt(t *testing.T) {
	for _, kind := range []string{"initial_lease", "wrong_snapshot", "spec", "content", "lease_lost", "lease_timeout"} {
		t.Run(kind, func(t *testing.T) {
			f := newSnapshotPreparationFixture(t)
			switch kind {
			case "initial_lease":
				f.denyInitial = true
			case "wrong_snapshot":
				f.wrongSnapshot = true
			case "spec":
				f.failSpec = true
			case "content":
				f.failContent = true
			case "lease_lost":
				f.loseLease = true
			case "lease_timeout":
				f.loseLease = true
				f.blockRenewal = true
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			receipt, err := prepareManagedSnapshot(ctx, f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session)
			if err == nil || receipt != (managedSnapshotPreparation{}) {
				t.Fatal("failure yielded preparation success", err)
			}
			if kind == "initial_lease" && len(f.calls) != 0 {
				t.Fatal("denied initial lease reached Helper")
			}
			if strings.HasPrefix(kind, "lease_") && !errors.Is(err, errSnapshotLeaseLost) {
				t.Fatal("lost nonterminal lease classification", err)
			}
			if kind == "wrong_snapshot" && !reflect.DeepEqual(f.calls, []string{"snapshot"}) {
				t.Fatal("foreign snapshot reached Helper")
			}
		})
	}
}
