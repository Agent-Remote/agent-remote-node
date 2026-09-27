package runtimehelper

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
)

func (e Engine) prepareManagedSessionSpec(ctx context.Context, request Request) (map[string]any, error) {
	var input ManagedSessionSpecRequest
	if err := decodeStrictPayload(request.Payload, &input); err != nil {
		return nil, err
	}
	if err := input.validate(e.config.NodeID); err != nil {
		return nil, err
	}
	payload, err := Map(input.Session)
	if err != nil {
		return nil, err
	}
	if err := e.preflightManagedSpec(input, payload); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := e.requireManagedAccountDirectory(input.Snapshot); err != nil {
		return nil, err
	}
	digest, err := managedSpecInputDigest(request.RequestID, input, e.config)
	if err != nil {
		return nil, err
	}
	expected := skillmanager.SessionSpecIntent{Version: 1, Identity: input.Snapshot, InputDigest: digest, State: "started"}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if _, err := skillmanager.ReadAccountFence(store, input.Snapshot.NodeID, input.Snapshot.UserID, input.Snapshot.AccountID); err != nil {
		return nil, errors.New("managed spec requires the original closed account fence")
	}
	saved, intentErr := skillmanager.ReadSessionSpecIntent(store, expected)
	if intentErr != nil && !errors.Is(intentErr, os.ErrNotExist) {
		return nil, intentErr
	}
	if intentErr == nil && saved.State == "ready" {
		spec, err := e.loadSpec(input.Snapshot.SessionID)
		if err != nil {
			return nil, err
		}
		if err := e.requireManagedSpecReceipt(spec); err != nil {
			return nil, err
		}
		return managedSpecResult(spec), nil
	}
	if err := e.requireUnstartedManagedSpec(ctx, input.Snapshot.SessionID); err != nil {
		return nil, err
	}
	if _, err := store.Lstat("session-" + input.Snapshot.SessionID); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("pending spec creation cannot replace retained session work")
	}
	if errors.Is(intentErr, os.ErrNotExist) {
		if _, err := os.Lstat(filepath.Dir(e.specPath(input.Snapshot.SessionID))); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("managed spec creation cannot adopt an existing session directory")
		}
		if err := skillmanager.BeginSessionSpecIntent(store, expected); err != nil {
			return nil, err
		}
	}
	draft, draftErr := skillmanager.ReadSessionSpecDraft(store, expected)
	var spec SessionSpec
	if draftErr == nil {
		if err := decodeStrictJSON(draft, &spec); err != nil || e.managedSpecIntent(spec) != expected {
			return nil, errors.New("managed spec draft does not match its original input")
		}
		if spec.ManagedSkills.SnapshotInputDigest != input.SnapshotInputDigest || spec.BootID != currentBootID() {
			return nil, errors.New("managed spec draft requires original-boot recovery")
		}
		if err := e.requireManagedSpecIdentity(spec); err != nil {
			return nil, err
		}
		if err := e.saveManagedSpec(spec); err != nil {
			return nil, err
		}
		if err := e.grantSpecAccess(spec); err != nil {
			return nil, err
		}
	} else {
		if !errors.Is(draftErr, os.ErrNotExist) {
			return nil, draftErr
		}
		if _, err := os.Lstat(e.specPath(input.Snapshot.SessionID)); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("managed session spec exists without its original private draft")
		}
		spec, err = e.createManagedSpec(ctx, input, payload, digest)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	specDigest, err := managedSpecDigest(spec)
	if err != nil {
		return nil, err
	}
	if err := skillmanager.FinishSessionSpecIntent(store, expected, specDigest); err != nil {
		return nil, err
	}
	return managedSpecResult(spec), nil
}

func (e Engine) createManagedSpec(ctx context.Context, input ManagedSessionSpecRequest, payload map[string]any, digest string) (SessionSpec, error) {
	s := input.Session
	workspace := filepath.Join(e.config.WorkspaceRoot, s.UserID, "workspaces", s.WorkspaceID, "files")
	account := filepath.Join(e.config.AccountRoot, s.UserID, "tool-accounts", "claude", s.ToolAccountID)
	for _, prepare := range []func() error{
		func() error { return e.prepareSyncedWorkspace(s.UserID, workspace) },
		func() error { return e.ensureIndependentGitIndex(s.UserID, workspace) },
	} {
		if err := ctx.Err(); err != nil {
			return SessionSpec{}, err
		}
		if err := prepare(); err != nil {
			return SessionSpec{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return SessionSpec{}, err
	}
	return e.buildSpecWithManagedBinding(payload, s.SessionID, s.UserID, s.ToolAccountID, workspace, account, s.Argv, "session", input.Snapshot.SnapshotID,
		ManagedSessionSpecBinding{TaskID: input.Snapshot.TaskID, SnapshotInputDigest: input.SnapshotInputDigest, RequestDigest: digest})
}

func (e Engine) preflightManagedSpec(input ManagedSessionSpecRequest, payload map[string]any) error {
	if _, err := toolsessions.DecodeCreatePayload(payload); err != nil {
		return err
	}
	if !safeTimezone(input.Session.Timezone) || !safeLocale(input.Session.Locale) || !localeAvailable(input.Session.Locale) || validateName(input.Session.TmuxSessionName, "tmux_session_name") != nil {
		return errors.New("invalid managed session locale or tmux identity")
	}
	if _, err := parseRuntimePolicy(payload["runtime_policy"]); err != nil {
		return err
	}
	ego, err := parseEgoBrowserRuntimeContext(payload, e.config)
	if err != nil {
		return err
	}
	protocol, err := parseDeviceControlProtocol(payload["device_control"], "session")
	if err != nil {
		return err
	}
	if protocol != 0 {
		if _, err := managedDeviceControlArgv(input.Session.SessionID, input.Session.Argv); err != nil {
			return err
		}
	}
	return verifySkillSystemPins(SessionSpec{
		EgoBrowserEnabled: ego.Enabled, EgoBrowserWrapperPath: ego.WrapperPath, EgoBrowserWrapperVersion: ego.WrapperVersion,
		EgoBrowserSkillPath: ego.SkillPath, EgoBrowserSkillVersion: ego.SkillVersion, EgoBrowserSkillTreeSHA256: ego.SkillTreeSHA256,
		DeviceControlProtocolVersion: protocol, DeviceProxyPath: e.config.DeviceProxyPath,
	}, input.SystemReleases)
}

func (e Engine) requireUnstartedManagedSpec(ctx context.Context, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	state, err := readNativeUnit(ctx, e.config.SystemctlPath, "agent-remote-session-"+shortDigest(sessionID, 12)+".service")
	if err != nil || state.LoadState != "not-found" {
		return errors.New("managed spec creation cannot prove the original runtime is absent")
	}
	return nil
}

func (e Engine) managedSpecIntent(spec SessionSpec) skillmanager.SessionSpecIntent {
	return skillmanager.SessionSpecIntent{Version: 1, Identity: skillmanager.SkillSnapshotIdentity{
		SnapshotID: spec.SkillSnapshotID, TaskID: spec.ManagedSkills.TaskID, NodeID: e.config.NodeID,
		UserID: spec.UserID, AccountID: filepath.Base(spec.AccountPath), SessionID: spec.SessionID, RuntimeBackend: "native",
	}, InputDigest: spec.ManagedSkills.RequestDigest, State: "started"}
}

func (e Engine) requireManagedSpecIdentity(spec SessionSpec) error {
	identity, err := e.lookupIdentity(spec.UserID)
	if err != nil || identity.UID <= 0 || identity.UID != spec.RuntimeUID || identity.GID != spec.RuntimeGID || identity.Username != spec.Username {
		return errors.New("managed session runtime identity changed")
	}
	return nil
}

func (e Engine) requireManagedSpecReceipt(spec SessionSpec) error {
	if spec.BootID == "" || spec.BootID != currentBootID() {
		return errors.New("managed session spec requires original-boot recovery")
	}
	if err := e.requireManagedSpecIdentity(spec); err != nil {
		return err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	expected := e.managedSpecIntent(spec)
	receipt, err := skillmanager.ReadSessionSpecIntent(store, expected)
	if err != nil || receipt.State != "ready" {
		return errors.New("managed session spec creation is incomplete")
	}
	digest, err := managedSpecDigest(spec)
	if err != nil || digest != receipt.SpecDigest {
		return errors.New("managed session spec differs from its original receipt")
	}
	if _, err := skillmanager.ReadSessionSpecDraft(store, expected); err != nil {
		return err
	}
	return e.verifyManagedSpecFiles(spec)
}

func managedSpecResult(spec SessionSpec) map[string]any {
	return map[string]any{"status": "spec_ready", "session_id": spec.SessionID, "skill_snapshot_id": spec.SkillSnapshotID, "task_record_id": spec.ManagedSkills.TaskID, "runtime_backend": "native", "runtime_uid": spec.RuntimeUID}
}

// Account ownership and ACLs were established by binding/takeover. Spec creation must
// never follow or repair runtime-controlled account directories with privileged writes.
func (e Engine) requireManagedAccountDirectory(identity skillmanager.SkillSnapshotIdentity) error {
	anchor, err := filepath.EvalSymlinks(e.config.AccountRoot)
	if err != nil {
		return err
	}
	fd, err := unix.Open(anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	for _, component := range []string{identity.UserID, "tool-accounts", "claude", identity.AccountID, ".claude"} {
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return errors.New("managed spec requires its existing account directories")
		}
		fd = next
	}
	return unix.Close(fd)
}
