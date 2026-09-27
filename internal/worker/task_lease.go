package worker

import (
	"context"
	"errors"
	"time"
)

type taskLeaseTiming struct {
	ServerTime             time.Time
	LeaseUntil             time.Time
	RenewAfterMilliseconds int64
}

type taskLeaseRenewal func(context.Context) (taskLeaseTiming, error)

type taskLeaseBudget struct {
	deadline   time.Time
	renewAfter time.Duration
}

// withTaskLease cancels dependent work on any uncertain renewal and owns the complete loop lifetime.
// The caller must preserve retained content and must not turn this error into a cached terminal failure.
func withTaskLease(ctx context.Context, lost error, renew taskLeaseRenewal, run func(context.Context) error) error {
	if run == nil {
		return errors.New("task lease requires a work function")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	initialCtx, cancelInitial := context.WithTimeout(ctx, 10*time.Second)
	budget, err := renewTaskBudget(initialCtx, lost, renew)
	cancelInitial()
	if cause := ctx.Err(); cause != nil {
		return cause
	}
	if err != nil {
		return err
	}
	workCtx, cancelWork := context.WithCancelCause(ctx)
	defer cancelWork(nil)
	renewalCtx, cancelRenewal := context.WithCancel(workCtx)
	defer cancelRenewal()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var err error
		budget, err = maintainTaskLease(renewalCtx, lost, renew, budget)
		if err != nil {
			cancelWork(lost)
		}
	}()
	workErr := run(workCtx)
	cancelRenewal()
	<-done
	if cause := context.Cause(workCtx); cause != nil {
		return cause
	}
	if !time.Now().Before(budget.deadline) {
		return lost
	}
	return workErr
}

func maintainTaskLease(ctx context.Context, lost error, renew taskLeaseRenewal, budget taskLeaseBudget) (taskLeaseBudget, error) {
	timer := time.NewTimer(budget.renewAfter)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return budget, nil
		case <-timer.C:
		}
		if !time.Now().Before(budget.deadline) {
			return budget, lost
		}
		renewalCtx, cancel := context.WithDeadline(ctx, budget.deadline)
		renewed, err := renewTaskBudget(renewalCtx, lost, renew)
		cancel()
		if ctx.Err() != nil {
			return budget, nil
		}
		if err != nil {
			return budget, lost
		}
		budget = renewed
		timer.Reset(budget.renewAfter)
	}
}

func renewTaskBudget(ctx context.Context, lost error, renew taskLeaseRenewal) (taskLeaseBudget, error) {
	started := time.Now()
	lease, err := renew(ctx)
	if err != nil {
		return taskLeaseBudget{}, lost
	}
	duration := lease.LeaseUntil.Sub(lease.ServerTime)
	remaining := time.Until(started.Add(duration))
	if lease.RenewAfterMilliseconds <= 0 || lease.RenewAfterMilliseconds > 300000 {
		return taskLeaseBudget{}, lost
	}
	interval := time.Duration(lease.RenewAfterMilliseconds) * time.Millisecond
	if duration <= 0 || duration > 300*time.Second || remaining <= 0 || interval <= 0 || interval >= duration {
		return taskLeaseBudget{}, lost
	}
	// Subtracting the whole round trip makes this budget independent of host wall-clock skew.
	if interval > remaining/3 {
		interval = remaining / 3
	}
	if interval <= 0 {
		return taskLeaseBudget{}, lost
	}
	return taskLeaseBudget{deadline: started.Add(duration), renewAfter: interval}, nil
}
