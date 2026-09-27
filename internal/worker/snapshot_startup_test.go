package worker

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

var _ snapshotStartupHelper = runtimehelper.Client{}

type snapshotStartupFixture struct {
	*snapshotPreparationFixture
	recovered     bool
	failRecovery  bool
	failAdmission bool
	failLaunch    bool
	failCleanup   bool
	lateLeaseLoss bool
	wrongUID      bool
	stopRenewal   atomic.Bool
	parentCancel  context.CancelFunc
}

func (f *snapshotStartupFixture) RenewSkillSnapshotLease(ctx context.Context, binding skillmanager.SkillSnapshotIdentity, attempt int64) (api.SkillSnapshotLease, error) {
	lease, err := f.snapshotPreparationFixture.RenewSkillSnapshotLease(ctx, binding, attempt)
	if f.stopRenewal.Load() {
		return api.SkillSnapshotLease{}, errors.New("original lease revoked")
	}
	return lease, err
}

func (f *snapshotStartupFixture) RecoverManagedSession(_ context.Context, task string, input runtimehelper.ManagedSessionSpecRequest) (map[string]any, error) {
	f.calls = append(f.calls, "recover")
	f.checkRequest(task, input)
	if f.failRecovery {
		return nil, &runtimehelper.Error{Code: "SKILL_START_PENDING"}
	}
	if f.recovered {
		return map[string]any{"status": "running", "runtime_uid": 12345}, nil
	}
	return map[string]any{"status": "not_started"}, nil
}

func (f *snapshotStartupFixture) StartManagedSession(ctx context.Context, task string, input runtimehelper.ManagedSessionSpecRequest) (map[string]any, error) {
	f.calls = append(f.calls, "start")
	f.checkRequest(task, input)
	if f.failLaunch {
		return nil, errors.New("lost launch response")
	}
	if f.lateLeaseLoss {
		f.stopRenewal.Store(true)
		<-ctx.Done()
	}
	if f.parentCancel != nil {
		f.parentCancel()
	}
	uid := 12345
	if f.wrongUID {
		uid++
	}
	// Deliberately return success even if authorization was lost after Helper became ready.
	return map[string]any{"status": "running", "runtime_uid": uid}, nil
}

func (f *snapshotStartupFixture) CancelManagedSession(ctx context.Context, task string, input runtimehelper.ManagedSessionSpecRequest) error {
	f.calls = append(f.calls, "cancel")
	f.checkRequest(task, input)
	if ctx.Err() != nil {
		f.t.Error("cleanup reused cancelled startup context")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		f.t.Error("cleanup lacks independent deadline")
	}
	if f.failCleanup {
		return errors.New("writer cleanup uncertain")
	}
	return nil
}

func (f *snapshotStartupFixture) checkRequest(task string, input runtimehelper.ManagedSessionSpecRequest) {
	f.t.Helper()
	expected, err := runtimehelper.NewManagedSessionSpecRequest(f.snapshot, f.session)
	if err != nil || task != "original-task" || !reflect.DeepEqual(input, expected) {
		f.t.Error("startup changed original request", err)
	}
}

func (f *snapshotStartupFixture) authorize(ctx context.Context, uid uint32) error {
	f.calls = append(f.calls, "authorize")
	if uid != 12345 || ctx.Err() != nil {
		f.t.Error("peer admitted with wrong identity or expired context")
	}
	if f.failAdmission {
		return errors.New("peer authorization failed")
	}
	return nil
}

func (f *snapshotStartupFixture) recover(ctx context.Context, uid uint32) error {
	f.calls = append(f.calls, "recover_peer")
	if uid != 12345 || ctx.Err() != nil {
		f.t.Error("peer recovered with wrong identity or expired context")
	}
	if f.failAdmission {
		return errors.New("original peer authorization unavailable")
	}
	return nil
}

func TestManagedSnapshotStartupKeepsOneLeaseAndRecoversBeforePreparation(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "historical"}[recovered], func(t *testing.T) {
			f := &snapshotStartupFixture{snapshotPreparationFixture: newSnapshotPreparationFixture(t), recovered: recovered}
			result, err := startManagedSnapshot(context.Background(), f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session, f)
			if err != nil || result["status"] != "running" {
				t.Fatal("startup failed", err)
			}
			if _, leaked := result["runtime_uid"]; leaked {
				t.Fatal("task result exposed internal runtime UID")
			}
			expected := []string{"snapshot", "recover", "spec", "prepare", "file", "authorize", "start"}
			if recovered {
				expected = []string{"snapshot", "recover", "recover_peer"}
			}
			if !reflect.DeepEqual(f.calls, expected) || f.renewals.Load() < 1 {
				t.Fatal("startup bypassed recovery/admission order", f.calls)
			}
		})
	}
}

func TestManagedSnapshotStartupFailuresStayNonterminalAndDrainOriginal(t *testing.T) {
	for _, kind := range []string{"initial_lease", "foreign_snapshot", "recovery", "spec", "content", "admission", "recovered_peer", "launch", "uid", "lease_after_ready", "cancel_after_ready", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &snapshotStartupFixture{snapshotPreparationFixture: newSnapshotPreparationFixture(t)}
			switch kind {
			case "initial_lease":
				f.denyInitial = true
			case "foreign_snapshot":
				f.wrongSnapshot = true
			case "recovery":
				f.failRecovery = true
			case "spec":
				f.failSpec = true
			case "content":
				f.failContent = true
			case "admission":
				f.failAdmission = true
			case "recovered_peer":
				f.recovered, f.failAdmission = true, true
			case "launch":
				f.failLaunch = true
			case "uid":
				f.wrongUID = true
			case "lease_after_ready":
				f.lateLeaseLoss = true
			case "cancel_after_ready":
				f.parentCancel = cancel
			case "cleanup":
				f.failLaunch, f.failCleanup = true, true
			}
			result, err := startManagedSnapshot(ctx, f, f, "original-task", f.snapshot.SkillSnapshotIdentity, 3, f.session, f)
			if result != nil || !errors.Is(err, errManagedSessionPending) {
				t.Fatal("uncertain startup became terminal success", err)
			}
			if kind == "initial_lease" || kind == "lease_after_ready" {
				if !errors.Is(err, errSnapshotLeaseLost) {
					t.Fatal("lost lease classification", err)
				}
			}
			if kind == "cancel_after_ready" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost caller cancellation", err)
			}
			if kind == "initial_lease" {
				if len(f.calls) != 0 {
					t.Fatal("denied authority reached Helper", f.calls)
				}
			} else if kind == "foreign_snapshot" {
				if !reflect.DeepEqual(f.calls, []string{"snapshot"}) {
					t.Fatal("foreign snapshot reached Helper", f.calls)
				}
			} else if f.calls[len(f.calls)-1] != "cancel" {
				t.Fatal("uncertain startup skipped original writer cleanup", f.calls)
			}
		})
	}
}
