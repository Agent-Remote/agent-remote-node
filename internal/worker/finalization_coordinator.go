package worker

import (
	"context"

	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
)

// Worker copies share one ledger instance and cancellable transfer gate. Separate ledger
// instances on the same file would each overwrite the other's in-memory snapshot.
type finalizationCoordinator struct {
	gate    chan struct{}
	journal finalizationTransferJournal
}

func newFinalizationCoordinator() *finalizationCoordinator {
	return &finalizationCoordinator{gate: make(chan struct{}, 1)}
}

func (c *finalizationCoordinator) acquire(ctx context.Context, path string) (finalizationTransferJournal, error) {
	if c == nil {
		return finalizationTransferJournal{}, errFinalizationPending
	}
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return finalizationTransferJournal{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		c.release()
		return finalizationTransferJournal{}, err
	}
	if c.journal.ledger == nil {
		store, err := ledger.Open(path + ".skill-finalizations")
		if err != nil {
			c.release()
			return finalizationTransferJournal{}, err
		}
		c.journal.ledger = store
	}
	return c.journal, nil
}

func (c *finalizationCoordinator) release() { <-c.gate }
