package runtimehelper

import (
	"fmt"
	"testing"
)

func TestAccountCopyErrorCodesRemainStable(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{{errAccountCopyPending, "STATE_COPY_PENDING"}, {errAccountCopyFailed, "STATE_COPY_FAILED"}, {errMigrationWritersUnknown, "STATE_MIGRATION_PENDING"}, {errMigrationFailed, "STATE_MIGRATION_FAILED"}} {
		if actual := classifyError(fmt.Errorf("wrapped: %w", test.err)); actual != test.code {
			t.Fatal(actual)
		}
	}
}
