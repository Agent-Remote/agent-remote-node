package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type takeoverLeaseFunc func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error)

func (f takeoverLeaseFunc) RenewSkillTakeoverLease(ctx context.Context, binding skillmanager.AccountTakeoverBinding, attempt int64) (api.SkillTakeoverLease, error) {
	return f(ctx, binding, attempt)
}

func shortTakeoverLease() api.SkillTakeoverLease {
	serverTime := time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)
	return api.SkillTakeoverLease{ServerTime: serverTime, LeaseUntil: serverTime.Add(180 * time.Millisecond), RenewAfterMilliseconds: 20}
}

func TestTakeoverLeaseKeepsWorkAliveAndStopsItsLoop(t *testing.T) {
	var calls atomic.Int32
	renewed := make(chan struct{})
	client := takeoverLeaseFunc(func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
		if calls.Add(1) == 3 {
			close(renewed)
		}
		return shortTakeoverLease(), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	marker := errors.New("preserved operation error")
	err := withTakeoverLease(ctx, client, skillmanager.AccountTakeoverBinding{}, 3, func(ctx context.Context) error {
		select {
		case <-renewed:
			return marker
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if !errors.Is(err, marker) || calls.Load() < 3 {
		t.Fatalf("lease did not preserve work: %v", err)
	}
	before := calls.Load()
	time.Sleep(45 * time.Millisecond)
	if calls.Load() != before {
		t.Fatal("renewal loop outlived its owner")
	}
}

func TestTakeoverLeaseCancelsWorkOnRenewalFailureOrDeadline(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "deadline"}[blocked], func(t *testing.T) {
			var calls atomic.Int32
			finished := make(chan struct{})
			client := takeoverLeaseFunc(func(ctx context.Context, _ skillmanager.AccountTakeoverBinding, _ int64) (api.SkillTakeoverLease, error) {
				if calls.Add(1) == 1 {
					return shortTakeoverLease(), nil
				}
				defer close(finished)
				if blocked {
					<-ctx.Done()
					return api.SkillTakeoverLease{}, ctx.Err()
				}
				return api.SkillTakeoverLease{}, errors.New("private upstream details")
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := withTakeoverLease(ctx, client, skillmanager.AccountTakeoverBinding{}, 3, func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			})
			if !errors.Is(err, errTakeoverLeaseLost) {
				t.Fatalf("uncertain authorization was accepted: %v", err)
			}
			select {
			case <-finished:
			default:
				t.Fatal("renewal request remains running")
			}
		})
	}
}

func TestTakeoverLeaseRejectsExhaustedResponseBudgetBeforeWork(t *testing.T) {
	client := takeoverLeaseFunc(func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
		lease := shortTakeoverLease()
		lease.LeaseUntil = lease.ServerTime.Add(5 * time.Millisecond)
		lease.RenewAfterMilliseconds = 1
		time.Sleep(15 * time.Millisecond)
		return lease, nil
	})
	called := false
	err := withTakeoverLease(context.Background(), client, skillmanager.AccountTakeoverBinding{}, 3, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, errTakeoverLeaseLost) || called {
		t.Fatal("exhausted lease started dependent work")
	}
}

func TestTakeoverLeaseCancelsInFlightRenewalWhenWorkEnds(t *testing.T) {
	var calls atomic.Int32
	started, ended := make(chan struct{}), make(chan struct{})
	client := takeoverLeaseFunc(func(ctx context.Context, _ skillmanager.AccountTakeoverBinding, _ int64) (api.SkillTakeoverLease, error) {
		if calls.Add(1) == 1 {
			return shortTakeoverLease(), nil
		}
		close(started)
		<-ctx.Done()
		close(ended)
		return api.SkillTakeoverLease{}, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := withTakeoverLease(ctx, client, skillmanager.AccountTakeoverBinding{}, 3, func(ctx context.Context) error {
		select {
		case <-started:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	default:
		t.Fatal("in-flight renewal was leaked")
	}
}

func TestTakeoverLeaseHonorsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := takeoverLeaseFunc(func(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error) {
		return shortTakeoverLease(), nil
	})
	err := withTakeoverLease(ctx, client, skillmanager.AccountTakeoverBinding{}, 3, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation lost: %v", err)
	}
}
