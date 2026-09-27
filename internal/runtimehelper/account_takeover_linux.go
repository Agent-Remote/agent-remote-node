package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

var errTakeoverWritersActive = errors.New("STATE_WRITERS_ACTIVE: legacy writers have not exited")
var errTakeoverWritersUnknown = errors.New("STATE_WRITERS_UNKNOWN: legacy writer quiescence cannot be established")

// The worker obtains exact live task authorization before invoking this capture.
// Calls must use the Helper's serialized mutation boundary; no caller-provided proof flag is trusted.
func (e Engine) captureNativeAccountTakeover(ctx context.Context, binding skillmanager.AccountTakeoverBinding, writers []skillmanager.AccountWriter) (skillmanager.AccountCapture, error) {
	if err := binding.Validate(); err != nil {
		return skillmanager.AccountCapture{}, err
	}
	digest, err := skillmanager.AccountInventoryDigest(writers)
	if err != nil || digest != binding.InventoryDigest || binding.NodeID != e.config.NodeID || binding.RuntimeBackend != "native" {
		return skillmanager.AccountCapture{}, errTakeoverWritersUnknown
	}
	if err := ctx.Err(); err != nil {
		return skillmanager.AccountCapture{}, err
	}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return skillmanager.AccountCapture{}, err
	}
	defer store.Close()
	fence, err := skillmanager.CloseAccountImports(store, skillmanager.AccountFence{
		Version: 1, NodeID: binding.NodeID, UserID: binding.UserID, AccountID: binding.AccountID, DirectoryEpoch: binding.DirectoryEpoch,
	})
	if err != nil || fence.DirectoryEpoch != binding.DirectoryEpoch {
		return skillmanager.AccountCapture{}, errTakeoverWritersUnknown
	}
	bundle, previous, _, err := skillmanager.OpenAccountCapture(store, binding)
	if err == nil {
		_ = bundle.Close()
		return previous, ctx.Err()
	}
	if !errors.Is(err, os.ErrNotExist) {
		return skillmanager.AccountCapture{}, err
	}
	if err := skillmanager.CheckAccountImportsDrained(store, binding.NodeID, binding.UserID, binding.AccountID); err != nil {
		return skillmanager.AccountCapture{}, errTakeoverWritersUnknown
	}
	if err := skillmanager.CheckAccountCopyHistory(store, binding.NodeID, binding.UserID, binding.AccountID); err != nil {
		return skillmanager.AccountCapture{}, errTakeoverWritersUnknown
	}
	if err := e.checkNativeAccountWriters(ctx, binding, writers); err != nil {
		return skillmanager.AccountCapture{}, err
	}
	source, err := e.openTakeoverSource(binding.UserID, binding.AccountID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return skillmanager.AccountCapture{}, err
	}
	if source != nil {
		defer source.Close()
	}
	policy := e.config.WithDefaults().SkillStatePolicy
	return skillmanager.CaptureAccountSource(ctx, store, source, binding, skillmanager.CopyPolicy{
		DirectoryBytes: policy.DirectoryBytes, Entries: policy.Entries,
		MinimumFreeBytes: policy.MinimumFreeBytes, ReservePercent: policy.ReservePercent,
	})
}

func (e Engine) openTakeoverSource(userID, accountID string) (*os.Root, error) {
	if _, err := os.Lstat(e.config.AccountRoot); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(e.config.AccountRoot)
	if err != nil {
		return nil, errors.New("account source root alias cannot be resolved")
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range []string{userID, "tool-accounts", "claude", accountID, ".claude", "skills"} {
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	defer unix.Close(fd)
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}
