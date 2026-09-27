package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskLeaseCannotCompleteAfterLastConfirmedBudget(t *testing.T) {
	var calls atomic.Int32
	blocked := make(chan struct{})
	release := make(chan struct{})
	lost := errors.New("test lease lost")
	renew := func(context.Context) (taskLeaseTiming, error) {
		if calls.Add(1) > 1 {
			close(blocked)
			<-release
		}
		now := time.Now()
		return taskLeaseTiming{ServerTime: now, LeaseUntil: now.Add(60 * time.Millisecond), RenewAfterMilliseconds: 1}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := withTaskLease(ctx, lost, renew, func(ctx context.Context) error {
		select {
		case <-blocked:
		case <-ctx.Done():
			return ctx.Err()
		}
		// A response finishing after its deadline cannot rescue already exhausted authority.
		time.Sleep(90 * time.Millisecond)
		close(release)
		return nil
	})
	if !errors.Is(err, lost) {
		t.Fatal("operation succeeded beyond confirmed deadline", err)
	}
}
