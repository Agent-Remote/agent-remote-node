package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func (s *Server) reclaimFinalization(ctx context.Context, capture skillmanager.FinalizationRecord, authorize finalizationReclamationAuthority) error {
	if err := s.lockSkillPreparation(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	return s.engine.reclaimFinalization(ctx, capture, authorize)
}

func (s *Server) resumeFinalizationReclamation(ctx context.Context, nodeID, sessionID string) (skillmanager.FinalizationRecord, error) {
	if nodeID != s.engine.config.NodeID || !validSkillUUID(nodeID) || !validSkillUUID(sessionID) {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	if err := s.lockSkillPreparation(ctx); err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	defer s.mu.Unlock()
	bundle, session, err := s.engine.retainedNativeSkillSession(sessionID)
	if err != nil {
		return skillmanager.FinalizationRecord{}, err
	}
	defer bundle.Close()
	intent, _, err := skillmanager.ReadFinalizationReclamation(bundle, session.Snapshot.Binding)
	if err != nil || intent == nil {
		return skillmanager.FinalizationRecord{}, errReclamationPending
	}
	return intent.Capture, s.engine.reclaimFinalization(ctx, intent.Capture, nil)
}

func (e Engine) reclaimFinalization(ctx context.Context, capture skillmanager.FinalizationRecord, authorize finalizationReclamationAuthority) error {
	if capture.Validate() != nil || capture.Binding.NodeID != e.config.NodeID || !capture.CanDeleteSession() || capture.ObjectsVersion != 1 {
		return errors.New("reclamation requires original terminal native capture")
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return err
	}
	defer store.Close()
	bundle, session, err := skillmanager.OpenSessionSnapshot(store, capture.Binding.SessionID)
	if err != nil {
		return err
	}
	defer bundle.Close()
	if session.Snapshot.Binding != capture.Binding || session.Runtime.Backend != "native" ||
		session.Runtime.ResourceID != "agent-remote-session-"+shortDigest(capture.Binding.SessionID, 12)+".service" {
		return errors.New("reclamation differs from original native session")
	}
	saved, err := skillmanager.ReadFinalization(bundle, capture.Binding)
	if err != nil || saved != capture {
		return errors.New("reclamation capture differs from retained input")
	}
	proof, err := e.finalizationReclamationProof(ctx, store, bundle, session, capture)
	if err != nil {
		return err
	}
	intent, _, err := skillmanager.ReadFinalizationReclamation(bundle, capture.Binding)
	if err != nil {
		return err
	}
	if intent == nil {
		if authorize == nil {
			return errors.New("unmarked reclamation requires fresh remote authorization")
		}
		ack, err := skillmanager.ReadFinalizationAcknowledgement(bundle, capture.Binding)
		if err != nil {
			return err
		}
		authority, deadline, err := authorize(ctx, ack)
		if err != nil {
			return err
		}
		markContext, cancel := context.WithDeadline(ctx, deadline)
		if err := proof.Verify(markContext); err != nil {
			cancel()
			return err
		}
		marked, err := skillmanager.MarkFinalizationReclamation(markContext, bundle, capture, authority, deadline, proof.spec.SessionRoot)
		cancel()
		if err != nil {
			return err
		}
		intent = &marked
	}
	if intent.SessionRoot != proof.spec.SessionRoot {
		return errors.New("reclamation changed original runtime root")
	}
	return skillmanager.ReclaimFinalizationContentWithChecks(ctx, bundle, *intent, skillmanager.ReclamationChecks{
		Verify: proof.Verify, Guard: proof.Guard,
	})
}

type finalizationReclamationProof struct {
	engine  Engine
	store   *os.Root
	bundle  *os.Root
	session skillmanager.SessionSnapshot
	capture skillmanager.FinalizationRecord
	spec    SessionSpec
	launch  skillmanager.SessionLaunch
	boot    string
}

func (e Engine) finalizationReclamationProof(ctx context.Context, store, bundle *os.Root, session skillmanager.SessionSnapshot, capture skillmanager.FinalizationRecord) (*finalizationReclamationProof, error) {
	boot := currentBootID()
	if !validSkillUUID(boot) {
		return nil, errors.New("reclamation requires a stable kernel boot")
	}
	spec, err := e.previousBootSkillSpec(store, session)
	if err != nil {
		return nil, err
	}
	launch, err := skillmanager.ReadRetainedSessionLaunch(store, session)
	if err != nil && !(boot != session.Runtime.BootID && errors.Is(err, os.ErrNotExist)) {
		return nil, err
	}
	proof := &finalizationReclamationProof{engine: e, store: store, bundle: bundle, session: session, capture: capture, spec: spec, launch: launch, boot: boot}
	if err := proof.Verify(ctx); err != nil {
		return nil, err
	}
	return proof, nil
}

// Guard remains cheap enough for each entry. The caller's lifecycle lock excludes Helper launch,
// mount and namespace mutations; the executor separately verifies entry inodes and mount IDs.
func (p *finalizationReclamationProof) Guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if currentBootID() != p.boot {
		return errors.New("kernel boot changed during reclamation")
	}
	if _, err := os.Lstat(p.spec.SessionRoot); !errors.Is(err, os.ErrNotExist) {
		return errors.New("reclamation runtime root is present or uncertain")
	}
	group := "/system.slice/" + p.spec.UnitName
	if p.boot == p.session.Runtime.BootID {
		return confirmEmptyCgroup(p.engine.config.CgroupRoot, group)
	}
	groups, err := os.OpenRoot(p.engine.config.CgroupRoot)
	if err != nil {
		return err
	}
	defer groups.Close()
	if _, err := groups.Lstat(group[1:]); !errors.Is(err, os.ErrNotExist) {
		return errors.New("previous-boot reclamation cgroup is present or uncertain")
	}
	return ctx.Err()
}

// Verify performs passive external inspection at deletion phase boundaries, never per file.
// In particular, historical cleanup and absent transient specs do not prove original invocation.
func (p *finalizationReclamationProof) Verify(ctx context.Context) error {
	if err := p.Guard(ctx); err != nil {
		return err
	}
	current, err := p.engine.previousBootSkillSpec(p.store, p.session)
	if err != nil || !reflect.DeepEqual(current, p.spec) {
		return errors.New("reclamation lost original spec authority")
	}
	launch, err := skillmanager.ReadRetainedSessionLaunch(p.store, p.session)
	if err != nil && !(p.launch == (skillmanager.SessionLaunch{}) && errors.Is(err, os.ErrNotExist)) || launch != p.launch {
		return errors.New("reclamation lost original launch authority")
	}
	cleaned, err := skillmanager.FinalizationRuntimeCleaned(p.bundle, p.capture, p.spec.SessionRoot)
	if err != nil || !cleaned {
		return errors.New("reclamation requires original runtime cleanup receipt")
	}
	state, err := readManagedNativeUnit(ctx, p.engine.config.SystemctlPath, p.spec.UnitName)
	if err != nil || requireRetainedInvocation(state, p.spec, p.launch.InvocationID) != nil ||
		state.ActiveState != "inactive" && state.ActiveState != "failed" ||
		p.boot != p.session.Runtime.BootID && state.LoadState != "not-found" {
		return errors.New("reclamation original unit is present or uncertain")
	}
	present, err := p.engine.finalizedNetworkPresent(ctx, p.spec.NetworkNamespace)
	if err != nil || present {
		return errors.New("reclamation runtime network is present or uncertain")
	}
	bundlePath := filepath.Join(p.engine.config.SkillStateRoot, "session-"+p.spec.SessionID)
	if err := requireNoRebootMounts(ctx, p.spec.SessionRoot, filepath.Join(bundlePath, "work"), filepath.Join(bundlePath, "finalization", "objects")); err != nil {
		return err
	}
	if err := p.Guard(ctx); err != nil {
		return err
	}
	after, err := readManagedNativeUnit(ctx, p.engine.config.SystemctlPath, p.spec.UnitName)
	if err != nil || after != state {
		return errors.New("reclamation unit changed during passive inspection")
	}
	return ctx.Err()
}
