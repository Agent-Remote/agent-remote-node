package runtimehelper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func (e Engine) cleanupFinalization(ctx context.Context, request Request) (skillmanager.FinalizationRecord, error) {
	capture, err := validateFinalizationCleanup(ctx, request, e.config.NodeID)
	if err != nil {
		return capture, err
	}
	bundle, session, err := e.retainedNativeSkillSession(capture.Binding.SessionID)
	if err != nil {
		return capture, err
	}
	defer bundle.Close()
	if session.Snapshot.Binding != capture.Binding {
		return capture, errors.New("cleanup changed retained session binding")
	}
	root := filepath.Join(e.config.StateRoot, "sessions", capture.Binding.SessionID)
	cleaned, err := skillmanager.FinalizationRuntimeCleaned(bundle, capture, root)
	if err != nil {
		return capture, err
	}
	if cleaned {
		// Historical completion never removes a resource that reappeared under the same ID.
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			return capture, errors.New("runtime root appeared after completed cleanup")
		}
		if boot := currentBootID(); session.Runtime.BootID != boot {
			spec := SessionSpec{SessionID: capture.Binding.SessionID, SessionRoot: root,
				NetworkNamespace: "ar-" + shortDigest(capture.Binding.SessionID, 10)}
			if err := e.verifyPreviousBootSkillResources(ctx, session, spec, boot); err != nil {
				return capture, err
			}
			return capture, skillmanager.RetainFinalizationRuntimeCleanup(bundle, capture, root)
		}
		if err := e.verifyFinalizedNativeWriters(ctx, session.Runtime); err != nil {
			return capture, err
		}
		present, err := e.finalizedNetworkPresent(ctx, "ar-"+shortDigest(capture.Binding.SessionID, 10))
		if err != nil || present {
			return capture, errors.New("runtime network appeared after completed cleanup")
		}
		return capture, skillmanager.RetainFinalizationRuntimeCleanup(bundle, capture, root)
	}
	boot := currentBootID()
	if !validSkillUUID(boot) {
		return capture, errors.New("current kernel boot identity is unavailable")
	}
	if session.Runtime.BootID != boot {
		return capture, e.cleanupPreviousBootSkillSession(ctx, bundle, session, capture, root, boot)
	}
	if err := e.verifyFinalizedNativeWriters(ctx, session.Runtime); err != nil {
		return capture, err
	}
	spec, err := e.loadSpec(capture.Binding.SessionID)
	if errors.Is(err, os.ErrNotExist) {
		spec = SessionSpec{SessionID: capture.Binding.SessionID, SkillSnapshotID: capture.Binding.SnapshotID, SessionRoot: root,
			NetworkNamespace: "ar-" + shortDigest(capture.Binding.SessionID, 10)}
	} else if err != nil {
		return capture, err
	} else if e.validateNativeSkillBinding(spec, capture.Binding) != nil || spec.BootID != session.Runtime.BootID || spec.UnitName != session.Runtime.ResourceID || spec.RuntimeUID != session.Runtime.UID || spec.RuntimeGID != session.Runtime.GID {
		return capture, errors.New("cleanup runtime spec differs from retained identity")
	}
	if err := e.cleanupNativeSkillMount(spec); err != nil {
		return capture, err
	}
	if err := cleanupFinalizedTemp(spec); err != nil {
		return capture, err
	}
	if err := e.cleanupFinalizedNetwork(ctx, spec.NetworkNamespace); err != nil {
		return capture, err
	}
	if err := ctx.Err(); err != nil {
		return capture, err
	}
	if err := removeFinalizedRuntimeRoot(root); err != nil {
		return capture, err
	}
	// Retain a durable historical completion before returning, independently of worker restart.
	return capture, skillmanager.RetainFinalizationRuntimeCleanup(bundle, capture, root)
}

func removeFinalizedRuntimeRoot(root string) error {
	parent, err := openSkillMountParent(SessionSpec{SessionRoot: filepath.Dir(root)})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return parent.Sync()
}

func cleanupFinalizedTemp(spec SessionSpec) error {
	parent, err := openSkillMountParent(spec)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	mounted, err := sessionMountPresent(parent, "tmp")
	if err != nil || !mounted {
		return err
	}
	target := fmt.Sprintf("/proc/self/fd/%d/tmp", parent.Fd())
	var stat unix.Statfs_t
	if err := unix.Statfs(target, &stat); err != nil || stat.Type != unix.TMPFS_MAGIC {
		return errors.New("runtime tmp mount has unexpected filesystem")
	}
	if err := unix.Unmount(target, 0); err != nil {
		return errors.New("runtime tmp mount remains busy or unavailable")
	}
	mounted, err = sessionMountPresent(parent, "tmp")
	if err != nil {
		return err
	}
	if mounted {
		return errors.New("runtime tmp mount remains attached")
	}
	return nil
}

func (e Engine) verifyFinalizedNativeWriters(ctx context.Context, runtime skillmanager.RuntimeBinding) error {
	state, err := readNativeUnit(ctx, e.config.SystemctlPath, runtime.ResourceID)
	if err != nil {
		return err
	}
	if state.ActiveState != "inactive" && state.ActiveState != "failed" {
		return errors.New("runtime cleanup cannot stop or adopt a live unit")
	}
	group := state.ControlGroup
	if group == "" {
		group = "/system.slice/" + runtime.ResourceID
	}
	if !validSessionCgroup(group, runtime.ResourceID) {
		return errors.New("runtime cleanup has an unexpected control group")
	}
	return confirmEmptyCgroup(e.config.CgroupRoot, group)
}

func (e Engine) cleanupFinalizedNetwork(ctx context.Context, namespace string) error {
	present, err := e.finalizedNetworkPresent(ctx, namespace)
	if err != nil || !present {
		return err
	}
	if err := runCommand(ctx, e.config.IPPath, "netns", "delete", namespace); err != nil {
		return errors.New("finalized network cleanup failed")
	}
	present, err = e.finalizedNetworkPresent(ctx, namespace)
	if err != nil {
		return err
	}
	if present {
		return errors.New("finalized network namespace remains")
	}
	return nil
}

func (e Engine) finalizedNetworkPresent(ctx context.Context, namespace string) (bool, error) {
	output := &boundedUnitOutput{remaining: 64 * 1024}
	command := exec.CommandContext(ctx, e.config.IPPath, "netns", "list")
	command.Stdout, command.Stderr = output, io.Discard
	if err := command.Run(); err != nil || output.overflow {
		return false, errors.New("finalized network inventory is unavailable")
	}
	for _, line := range strings.Split(output.buffer.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if (len(fields) != 1 && len(fields) != 3) || strings.ContainsAny(fields[0], "/\\\x00") || fields[0] == "." || fields[0] == ".." {
			return false, errors.New("finalized network inventory is malformed")
		}
		if len(fields) == 3 {
			id, err := strconv.ParseUint(strings.TrimSuffix(fields[2], ")"), 10, 32)
			if fields[1] != "(id:" || !strings.HasSuffix(fields[2], ")") || err != nil || id > 2147483647 {
				return false, errors.New("finalized network inventory identifier is malformed")
			}
		}
		if len(fields) > 0 && fields[0] == namespace {
			return true, nil
		}
	}
	return false, nil
}
