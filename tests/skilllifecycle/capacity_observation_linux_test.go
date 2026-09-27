package skilllifecycle

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

// Only fixed states and filesystem counters leave the disposable fixture; no IDs or file contents.
func observeCapacityFinalization(t *testing.T, data []byte, readErr error) {
	t.Helper()
	state := "invalid"
	if os.IsNotExist(readErr) {
		state = "absent"
	} else if readErr != nil {
		state = "read_error"
	} else {
		var record skillmanager.FinalizationRecord
		if json.Unmarshal(data, &record) == nil && record.Validate() == nil {
			switch record.State {
			case "local_durable", "upload_pending", "persisted", "persisted_unclean", "published", "conflicted", "detached", "superseded":
				state = record.State
			}
		}
	}
	var disk unix.Statfs_t
	if unix.Statfs(skillRoot, &disk) != nil || disk.Bsize <= 0 {
		t.Logf("capacity_observation=%s disk_known=false free_bytes=0 free_inodes=0", state)
		return
	}
	t.Logf("capacity_observation=%s disk_known=true free_bytes=%d free_inodes=%d", state,
		disk.Bavail*uint64(disk.Bsize), disk.Ffree)
}
