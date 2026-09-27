package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

var errTakeoverLeaseLost = errors.New("TAKEOVER_LEASE_LOST: takeover task authorization could not be maintained")

type takeoverLeaseClient interface {
	RenewSkillTakeoverLease(context.Context, skillmanager.AccountTakeoverBinding, int64) (api.SkillTakeoverLease, error)
}

// withTakeoverLease cancels work whenever the exact takeover lease becomes uncertain.
func withTakeoverLease(ctx context.Context, client takeoverLeaseClient, binding skillmanager.AccountTakeoverBinding, attempt int64, run func(context.Context) error) error {
	return withTaskLease(ctx, errTakeoverLeaseLost, func(ctx context.Context) (taskLeaseTiming, error) {
		lease, err := client.RenewSkillTakeoverLease(ctx, binding, attempt)
		return taskLeaseTiming{lease.ServerTime, lease.LeaseUntil, lease.RenewAfterMilliseconds}, err
	}, run)
}
