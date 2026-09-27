package runtimehelper

import (
	"bytes"
	"context"
	"encoding/hex"
	"reflect"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) inspectOriginalMigrationParents(ctx context.Context, original skillmanager.AccountMigrationReceipt, baseline skillmanager.AccountMigrationBaseline, phases migrationPhaseSet, source string) ([]skillmanager.MigrationParentPermissions, error) {
	return e.inspectMigrationRestorationParents(ctx, original, baseline, phases, source, false)
}

func (e Engine) inspectMigrationRestorationParents(ctx context.Context, original skillmanager.AccountMigrationReceipt, baseline skillmanager.AccountMigrationBaseline, phases migrationPhaseSet, source string, restorationStarted bool) ([]skillmanager.MigrationParentPermissions, error) {
	targetTraversal, rollbackTraversal := phases[3].Identity.Phase != "", phases[6].Identity.Phase != ""
	var native runtimeIdentity
	if targetTraversal && source == "docker_sandbox" || rollbackTraversal && source == "native" {
		var err error
		native, err = e.lookupIdentity(original.Copy.UserID)
		if err != nil || native.UID <= 0 {
			return nil, errMigrationWritersUnknown
		}
	}
	worker, err := e.dockerRuntimeIdentity()
	if err != nil {
		return nil, errMigrationWritersUnknown
	}
	sourceUID, targetUID := worker.UID, native.UID
	if source == "native" {
		sourceUID, targetUID = native.UID, worker.UID
	}
	observed := make([]skillmanager.MigrationParentPermissions, 0, len(baseline.Parents))
	for _, parent := range baseline.Parents {
		states, err := migrationParentAccessStates(parent, uint32(targetUID), uint32(sourceUID), uint32(worker.UID), targetTraversal, rollbackTraversal, restorationStarted)
		if err != nil {
			return nil, errMigrationWritersUnknown
		}
		current, err := inspectMigrationParentRestoration(ctx, parent, states)
		if err != nil {
			return nil, errMigrationWritersUnknown
		}
		observed = append(observed, current)
	}
	return observed, nil
}

func inspectMigrationParentRestoration(ctx context.Context, original skillmanager.MigrationParentPermissions, states []migrationParentAccess) (skillmanager.MigrationParentPermissions, error) {
	scanner := migrationInventoryScanner{ctx: ctx, xattrBuffer: make([]byte, 64<<10)}
	current, err := scanner.parentPermissions(original.Path)
	if err != nil || current.Identity != original.Identity || current.UID != original.UID || current.GID != original.GID ||
		!bytes.Equal(current.DefaultACL, original.DefaultACL) || !matchesMigrationParentAccess(current, states) {
		return current, errMigrationInventory
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, original.Path, &unix.OpenHow{
		Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return current, errMigrationInventory
	}
	defer unix.Close(fd)
	projected, err := scanner.readXattrs(fd, fd, ".", false, &original.AccessACL)
	if err != nil || hex.EncodeToString(projected.all[:]) != original.AttributeDigest {
		return current, errMigrationInventory
	}
	after, err := scanner.parentPermissions(original.Path)
	if err != nil || !reflect.DeepEqual(current, after) || ctx.Err() != nil {
		return current, errMigrationInventory
	}
	return current, nil
}

func verifyMigrationParentObservation(ctx context.Context, fd int, expected skillmanager.MigrationParentPermissions) error {
	before, err := migrationInventoryStat(fd)
	if err != nil || migrationObjectIdentity(before) != expected.Identity || uint32(before.Mode) != expected.Mode || before.Uid != expected.UID || before.Gid != expected.GID {
		return errMigrationInventory
	}
	scanner := migrationInventoryScanner{ctx: ctx, xattrBuffer: make([]byte, 64<<10)}
	attributes, err := scanner.xattrs(fd, fd, ".", false)
	if err != nil || hex.EncodeToString(attributes.all[:]) != expected.AttributeDigest {
		return errMigrationInventory
	}
	after, err := migrationInventoryStat(fd)
	if err != nil || after != before || ctx.Err() != nil {
		return errMigrationInventory
	}
	return nil
}
