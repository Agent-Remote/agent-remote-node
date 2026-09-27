package runtimehelper

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) applyMigrationOwnership(ctx context.Context, copy skillmanager.AccountCopyReceipt, phase, userID, accountPath, backend string) error {
	if ctx.Err() != nil {
		return errMigrationWritersUnknown
	}
	store, err := e.openSkillStateRoot()
	if err != nil {
		return errMigrationWritersUnknown
	}
	intentErr := skillmanager.BeginMigrationOwnership(store, skillmanager.MigrationOwnershipIntent{Version: 1, Copy: copy, Phase: phase, Backend: backend})
	_ = store.Close()
	if intentErr != nil {
		return errMigrationWritersUnknown
	}
	var identity runtimeIdentity
	if backend == "native" {
		identity, err = e.ensureIdentity(userID)
	} else {
		identity, err = e.dockerRuntimeIdentity()
	}
	if err != nil {
		return err
	}
	if identity.UID <= 0 || identity.GID <= 0 {
		return errors.New("invalid migration runtime identity")
	}
	if err := filepath.WalkDir(accountPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return os.Lchown(path, identity.UID, identity.GID)
	}); err != nil {
		return err
	}
	setfacl, err := exec.LookPath(e.config.SetfaclPath)
	if err != nil {
		return err
	}
	if setfacl, err = filepath.Abs(setfacl); err != nil {
		return err
	}
	access := namedUserACL(identity.Username, e.config.NodeUser, "rwX")
	commands := [][]string{{"-R", "-P", "-m", access, "--", accountPath}, {"-m", namedDefaultUserACL(identity.Username, e.config.NodeUser, "rwX"), "--", accountPath}}
	traverse := []string{"-m", namedUserACL(identity.Username, e.config.NodeUser, "--x"), "--"}
	traverse = append(traverse, e.migrationTraversalParents(accountPath)...)
	commands = append(commands, traverse)
	for index, args := range commands {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, drained := e.runMigrationWriter(ctx, copy, phase+"-"+strconv.Itoa(index), setfacl, args...)
		if !drained {
			return errMigrationWritersUnknown
		}
		if !ok {
			return errors.New("migration ownership command failed")
		}
	}
	return nil
}

// Flush both filesystems before a durable terminal record can outlive account/backup metadata.
func syncMigrationFilesystems(paths ...string) error {
	for _, path := range paths {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		err = unix.Syncfs(fd)
		_ = unix.Close(fd)
		if err != nil {
			return err
		}
	}
	return nil
}
