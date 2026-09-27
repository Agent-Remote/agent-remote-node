package worker

import (
	"context"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

var errSnapshotLeaseLost = errors.New("SNAPSHOT_LEASE_LOST: managed session preparation authorization could not be maintained")

type snapshotLeaseClient interface {
	RenewSkillSnapshotLease(context.Context, skillmanager.SkillSnapshotIdentity, int64) (api.SkillSnapshotLease, error)
}

// Lease loss is nonterminal: retained preparation must remain available for the original task.
func withSnapshotLease(ctx context.Context, client snapshotLeaseClient, binding skillmanager.SkillSnapshotIdentity, attempt int64, run func(context.Context) error) error {
	return withTaskLease(ctx, errSnapshotLeaseLost, func(ctx context.Context) (taskLeaseTiming, error) {
		lease, err := client.RenewSkillSnapshotLease(ctx, binding, attempt)
		return taskLeaseTiming{lease.ServerTime, lease.LeaseUntil, lease.RenewAfterMilliseconds}, err
	}, run)
}
