package runtime

import (
	"os/exec"
	goruntime "runtime"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

// Snapshot captures current node resource and runtime status.
func Snapshot() (api.ResourceStatus, api.RuntimeStatus) {
	var mem goruntime.MemStats
	goruntime.ReadMemStats(&mem)

	resources := api.ResourceStatus{
		CPULoad:          0,
		MemoryUsedBytes:  int64(mem.Alloc),
		MemoryTotalBytes: int64(mem.Sys),
		DiskUsedBytes:    0,
		DiskTotalBytes:   0,
	}
	status := api.RuntimeStatus{
		DockerOK:              commandExists("docker"),
		TmuxOK:                commandExists("tmux"),
		ActiveSessions:        0,
		ActiveBrowserSessions: 0,
		Containers:            0,
	}
	return resources, status
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
