package runtimehelper

import (
	"context"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

// restoreOriginalMigrationPermissions is part of the first, serialized migration execution.
// Passive recovery never calls it. The original rollback intent already fences every direct
// ownership write; a cancelled or incomplete restoration cannot acquire a terminal attestation.
func (e Engine) restoreOriginalMigrationPermissions(ctx context.Context, store *os.Root, original skillmanager.AccountMigrationReceipt, source, account, backup string) error {
	if ctx.Err() != nil || original.Version != 2 || original.State != "started" || original.Copy.BootID != currentBootID() {
		return errMigrationWritersUnknown
	}
	saved, err := skillmanager.ReadAccountMigration(store, original)
	if err != nil || saved != original {
		return errMigrationWritersUnknown
	}
	rollback, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "rollback")
	if err != nil || rollback.Backend != source {
		return errMigrationWritersUnknown
	}
	target, err := skillmanager.ReadMigrationOwnership(store, original.Copy, "target")
	if err != nil || target.Backend == source {
		return errMigrationWritersUnknown
	}
	phases, err := e.refreshMigrationPhases(ctx, store, original.Copy)
	if err != nil || phases[0].State != "succeeded" {
		return errMigrationWritersUnknown
	}
	if _, err := completedMigrationOutcome(phases, true); err != nil {
		return errMigrationWritersUnknown
	}
	// Content and the backup's original permissions must match before the first write.
	before, err := e.inspectMigrationCompletion(ctx, store, original, account, backup, "succeeded")
	if err != nil {
		return err
	}
	baseline, err := skillmanager.ReadAccountMigrationBaseline(store, original)
	if err != nil {
		return errMigrationWritersUnknown
	}
	parents, err := e.inspectOriginalMigrationParents(ctx, original, baseline, phases, source)
	if err != nil {
		return err
	}
	if err := restoreMigrationTreePermissions(ctx, account, backup, before.inventory); err != nil {
		return errMigrationWritersUnknown
	}
	for index, parent := range baseline.Parents {
		if err := restoreMigrationParent(ctx, parent, parents[index]); err != nil {
			return errMigrationWritersUnknown
		}
	}
	if ctx.Err() != nil || currentBootID() != original.Copy.BootID {
		return errMigrationWritersUnknown
	}
	// Whole completion owns the subsequent sync, repeated inventory and attestation.
	_, err = e.inspectMigrationCompletion(ctx, store, original, account, backup, "failed")
	return err
}

func restoreMigrationParent(ctx context.Context, parent, observed skillmanager.MigrationParentPermissions) error {
	if ctx.Err() != nil {
		return errMigrationInventory
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, parent.Path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return errMigrationInventory
	}
	defer unix.Close(fd)
	stat, err := migrationInventoryStat(fd)
	// Migration only changes traversal ACLs on shared parents, never their ownership.
	if err != nil || migrationObjectIdentity(stat) != parent.Identity || stat.Uid != parent.UID || stat.Gid != parent.GID || ctx.Err() != nil {
		return errMigrationInventory
	}
	if observed.Path != parent.Path || verifyMigrationParentObservation(ctx, fd, observed) != nil {
		return errMigrationInventory
	}
	if err := unix.Fchmod(fd, parent.Mode&07777); err != nil {
		return err
	}
	for name, value := range map[string][]byte{"system.posix_acl_access": parent.AccessACL, "system.posix_acl_default": parent.DefaultACL} {
		if err := restoreMigrationAttribute(ctx, fd, name, value, value != nil); err != nil {
			return err
		}
	}
	return ctx.Err()
}
