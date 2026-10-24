package runtimehelper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

type sessionLifecycleIdentity struct{ session, backend string }

func (e Engine) lifecycleIdentity(request Request) (sessionLifecycleIdentity, bool, error) {
	identity := sessionLifecycleIdentity{}
	switch request.Operation {
	case managedSpecOperation, managedLaunchOperation, managedRecoveryOperation, managedCancelOperation:
		var input ManagedSessionSpecRequest
		if err := decodeStrictPayload(request.Payload, &input); err != nil {
			return identity, true, err
		}
		if err := input.validate(e.config.NodeID); err != nil {
			return identity, true, err
		}
		return sessionLifecycleIdentity{input.Snapshot.SessionID, "native"}, true, nil
	case "prepare_account", "docker_prepare_account":
		identity.session, _ = request.Payload["binding_id"].(string)
		identity.backend = "native"
		if request.Operation == "docker_prepare_account" {
			identity.backend = "docker_sandbox"
		}
		return identity, true, validateID(identity.session, "binding_id")
	case "start_session", "stop_session", "docker_start_session", "docker_stop_session", recordSessionStopOperation:
		identity.session, _ = request.Payload["session_id"].(string)
		identity.backend = "native"
		if request.Operation == "docker_start_session" || request.Operation == "docker_stop_session" {
			identity.backend = "docker_sandbox"
		}
		if request.Operation == recordSessionStopOperation {
			identity.backend, _ = request.Payload["runtime_backend"].(string)
		}
		if err := validateID(identity.session, "session_id"); err != nil {
			return identity, true, err
		}
		if identity.backend != "native" && identity.backend != "docker_sandbox" {
			return identity, true, errors.New("invalid session lifecycle backend")
		}
		if raw, present := request.Payload["runtime_backend"]; present && raw != identity.backend {
			return identity, true, errors.New("session lifecycle backend mismatch")
		}
		return identity, true, nil
	default:
		return identity, false, nil
	}
}

func sessionStopOperation(operation string) bool {
	return operation == "stop_session" || operation == "docker_stop_session" || operation == managedCancelOperation || operation == recordSessionStopOperation || operation == "cleanup_resources"
}

// validateSessionStopBinding observes only trusted specs. Absence grants no process authority.
func (e Engine) validateSessionStopBinding(identity sessionLifecycleIdentity, payload map[string]any) error {
	native, nativeErr := e.loadSpec(identity.session)
	if nativeErr == nil {
		if native.Kind != "session" || identity.backend != "native" {
			return errors.New("session stop does not match the original runtime kind/backend")
		}
		if resource, ok := payload["runtime_resource_id"].(string); ok && resource != "" && resource != native.UnitName {
			return errors.New("session stop resource identity mismatch")
		}
	} else if !errors.Is(nativeErr, os.ErrNotExist) {
		return nativeErr
	}
	docker, dockerErr := e.loadDockerSessionSpec(identity.session)
	if dockerErr == nil {
		if dockerSessionKind(docker) != dockerSessionKindTool || identity.backend != "docker_sandbox" {
			return errors.New("session stop does not match the original Docker kind/backend")
		}
		if resource, ok := payload["runtime_resource_id"].(string); ok && resource != "" && resource != docker.SandboxName {
			return errors.New("session stop resource identity mismatch")
		}
	} else if !errors.Is(dockerErr, os.ErrNotExist) {
		return dockerErr
	}
	if raw, present := payload["skill_finalization"]; present {
		pointer, ok := raw.(map[string]any)
		if !ok || len(pointer) != 4 {
			return errors.New("invalid finalization identity")
		}
		for _, field := range []string{"snapshot_id", "task_id", "user_id", "account_id"} {
			value, ok := pointer[field].(string)
			if !ok || validateID(value, field) != nil {
				return errors.New("invalid finalization identity")
			}
		}
		if nativeErr == nil && (pointer["snapshot_id"] != native.SkillSnapshotID || pointer["task_id"] != native.ManagedSkills.TaskID || pointer["user_id"] != native.UserID || pointer["account_id"] != filepath.Base(native.AccountPath)) {
			return errors.New("finalization identity differs from original spec")
		}
	}
	for _, field := range []string{"runtime_resource_id", "tmux_session_name", "sandbox_name"} {
		if raw, exists := payload[field]; exists {
			if _, ok := raw.(string); !ok {
				return errors.New("invalid session stop identity field")
			}
		}
	}
	return nil
}

func (e Engine) stopRequestIdentities(request Request) ([]sessionLifecycleIdentity, error) {
	if request.Version != ProtocolVersion || request.SessionLifecycleVersion != 0 && request.SessionLifecycleVersion != 1 || validateID(request.RequestID, "request_id") != nil {
		return nil, errors.New("invalid stop request")
	}
	if !sessionStopOperation(request.Operation) {
		return nil, nil
	}
	if request.Operation != "cleanup_resources" {
		identity, _, err := e.lifecycleIdentity(request)
		if err != nil {
			return nil, err
		}
		if err := e.validateSessionStopBinding(identity, request.Payload); err != nil {
			return nil, err
		}
		if err := e.validateManagedStopIntent(request, identity); err != nil {
			return nil, err
		}
		return []sessionLifecycleIdentity{identity}, nil
	}
	backend, ok := request.Payload["runtime_backend"].(string)
	if !ok || backend != "native" && backend != "docker_sandbox" {
		return nil, errors.New("invalid cleanup backend")
	}
	ids, ok := request.Payload["session_ids"].([]any)
	if !ok || len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("invalid cleanup batch")
	}
	identities := make([]sessionLifecycleIdentity, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, raw := range ids {
		id, ok := raw.(string)
		if !ok || validateID(id, "session_id") != nil || seen[id] {
			return nil, errors.New("invalid cleanup identity")
		}
		seen[id] = true
		identity := sessionLifecycleIdentity{id, backend}
		if err := e.validateSessionStopBinding(identity, nil); err != nil {
			return nil, err
		}
		if err := e.validateManagedStopIntent(request, identity); err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func (e Engine) prepareSessionStopIntents(ctx context.Context, request Request) error {
	if !sessionStopOperation(request.Operation) {
		return nil
	}
	identities, err := e.stopRequestIdentities(request)
	if err != nil {
		return err
	}
	if runtime.GOOS != "linux" && request.SessionLifecycleVersion == 0 {
		return nil
	}
	for _, identity := range identities {
		intent := sessionStopIntent{Version: 1, NodeID: e.config.NodeID, SessionID: identity.session, Scope: "tool_session", RequestID: request.RequestID, Operation: request.Operation, Backend: identity.backend}
		if err := e.publishSessionStop(ctx, intent); err != nil {
			return errors.Join(errSessionStopPending, err)
		}
	}
	return nil
}

func (e Engine) beginSessionExecution(ctx context.Context, request Request) (func(), error) {
	if request.SessionLifecycleVersion != 0 && request.SessionLifecycleVersion != 1 {
		return nil, errors.New("unsupported session lifecycle protocol")
	}
	if err := e.prepareSessionStopIntents(ctx, request); err != nil {
		return nil, err
	}
	identity, relevant, err := e.lifecycleIdentity(request)
	if err != nil {
		return nil, err
	}
	if !relevant {
		return func() {}, nil
	}
	if request.execution != nil {
		request.execution.Version = 1
		request.execution.SessionID = identity.session
		request.execution.Backend = identity.backend
	}
	if request.Operation == recordSessionStopOperation {
		return func() {}, nil
	}
	return e.lockSessionLifecycle(ctx, identity.session, "runtime")
}

func (e Engine) executeSessionResponse(ctx context.Context, request Request) (Response, error) {
	if request.SessionLifecycleVersion == 1 {
		request.execution = &SessionExecution{}
	}
	result, err := e.Execute(ctx, request)
	if err != nil {
		return Response{}, err
	}
	response := Response{Version: ProtocolVersion, OK: true, Result: result}
	if request.execution != nil {
		if request.execution.SessionID == "" {
			return Response{}, errors.New("session observation unavailable")
		}
		closed, err := e.sessionStopState(ctx, request.execution.SessionID)
		if err != nil {
			return Response{}, err
		}
		request.execution.StopIntent = closed
		request.execution.FreshAdmission = request.execution.FreshAdmission && !closed && !request.execution.ReceiptReplay
		response.SessionExecution = request.execution
	}
	return response, nil
}

func (e Engine) requireToolSessionSlot(session, backend string) error {
	if err := validateID(session, "session_id"); err != nil {
		return err
	}
	return e.validateSessionStopBinding(sessionLifecycleIdentity{session, backend}, nil)
}

func (e Engine) reobserveCachedStop(ctx context.Context, request Request) error {
	var err error
	switch request.Operation {
	case "stop_session":
		_, err = e.stopSession(ctx, request.Payload)
	case "docker_stop_session":
		_, err = e.dockerStopSession(request.Payload)
	case "cleanup_resources":
		_, err = e.cleanupResources(ctx, request.Payload)
	default:
		err = errSessionStopPending
	}
	if err != nil {
		return errors.Join(errSessionStopPending, err)
	}
	return nil
}

func (e Engine) requireBindingSessionSlot(session, backend string) error {
	if err := validateID(session, "binding_id"); err != nil {
		return err
	}
	native, err := e.loadSpec(session)
	if err == nil && (native.Kind != "binding" || backend != "native") {
		return errors.New("binding cannot replace a tool runtime")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	docker, err := e.loadDockerSessionSpec(session)
	if err == nil && (dockerSessionKind(docker) != dockerSessionKindBinding || backend != "docker_sandbox") {
		return errors.New("binding cannot replace a Docker tool runtime")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
