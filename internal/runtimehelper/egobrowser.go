package runtimehelper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
)

const (
	egoBrowserSandboxWrapperPath = "/opt/agent-remote/ego-browser/bin/ego-browser"
	egoBrowserSandboxSkillPath   = "/home/runtime/.claude/skills/ego-browser"
	egoBrowserSandboxSocketRoot  = "/run/agent-remote/ego-browser"
)

// egoBrowserRuntimeContext is the short-lived browser configuration carried by
// a session-start task. The broker nonce is intentionally excluded from the
// persisted SessionSpec JSON and is restored only from the supervisor env.
type egoBrowserRuntimeContext struct {
	Enabled         bool
	WrapperPath     string
	BrokerSocket    string
	BrokerNonce     string
	Protocol        string
	WrapperVersion  string
	SkillPath       string
	SkillVersion    string
	SkillTreeSHA256 string
	TaskSpace       string
}

// parseEgoBrowserRuntimeContext validates the worker-supplied browser context
// against root-owned static configuration. An omitted context is retained for
// older callers only when the bridge is disabled.
func parseEgoBrowserRuntimeContext(payload map[string]any, config EngineConfig) (egoBrowserRuntimeContext, error) {
	keys := []string{
		"ego_browser_enabled", "ego_browser_wrapper_path", "ego_browser_broker_socket",
		"ego_browser_broker_nonce", "ego_browser_protocol_version", "ego_browser_wrapper_version",
		"ego_browser_skill_path", "ego_browser_skill_version", "ego_browser_skill_tree_sha256",
		"ego_browser_task_space",
	}
	present := false
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
		if _, ok := payload[key]; ok {
			present = true
		}
	}
	for key := range payload {
		if strings.HasPrefix(key, "ego_browser_") {
			if _, ok := allowed[key]; !ok {
				return egoBrowserRuntimeContext{}, fmt.Errorf("unsupported ego-browser context field %q", key)
			}
		}
	}
	if !present {
		if egoBrowserContextRequired(payload, config) {
			return egoBrowserRuntimeContext{}, errors.New("ego-browser runtime context is required")
		}
		return egoBrowserRuntimeContext{}, nil
	}

	enabled, ok := payload["ego_browser_enabled"].(bool)
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_enabled is invalid")
	}
	context := egoBrowserRuntimeContext{Enabled: enabled}
	context.WrapperPath, ok = optionalContextString(payload, "ego_browser_wrapper_path")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_wrapper_path is invalid")
	}
	context.BrokerSocket, ok = optionalContextString(payload, "ego_browser_broker_socket")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_broker_socket is invalid")
	}
	context.BrokerNonce, ok = optionalContextString(payload, "ego_browser_broker_nonce")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_broker_nonce is invalid")
	}
	context.Protocol, ok = optionalContextString(payload, "ego_browser_protocol_version")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_protocol_version is invalid")
	}
	context.WrapperVersion, ok = optionalContextString(payload, "ego_browser_wrapper_version")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_wrapper_version is invalid")
	}
	context.SkillPath, ok = optionalContextString(payload, "ego_browser_skill_path")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_skill_path is invalid")
	}
	context.SkillVersion, ok = optionalContextString(payload, "ego_browser_skill_version")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_skill_version is invalid")
	}
	context.SkillTreeSHA256, ok = optionalContextString(payload, "ego_browser_skill_tree_sha256")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_skill_tree_sha256 is invalid")
	}
	context.TaskSpace, ok = optionalContextString(payload, "ego_browser_task_space")
	if !ok {
		return egoBrowserRuntimeContext{}, errors.New("ego_browser_task_space is invalid")
	}
	if !context.Enabled {
		if egoBrowserContextRequired(payload, config) {
			return egoBrowserRuntimeContext{}, errors.New("ego-browser runtime context is required")
		}
		if context.WrapperPath != "" || context.BrokerSocket != "" || context.BrokerNonce != "" ||
			context.Protocol != "" || context.WrapperVersion != "" || context.SkillPath != "" ||
			context.SkillVersion != "" || context.SkillTreeSHA256 != "" || context.TaskSpace != "" {
			return egoBrowserRuntimeContext{}, errors.New("disabled ego-browser context contains configured values")
		}
		return context, nil
	}
	if !config.EgoBrowserEnabled {
		return egoBrowserRuntimeContext{}, errors.New("ego-browser bridge is disabled")
	}
	if context.WrapperPath != config.EgoBrowserWrapperPath ||
		context.BrokerSocket != config.EgoBrowserBrokerSocket ||
		context.Protocol != config.EgoBrowserProtocolVersion ||
		context.WrapperVersion != config.EgoBrowserWrapperVersion ||
		context.SkillPath != config.EgoBrowserSkillPath ||
		context.SkillVersion != config.EgoBrowserSkillVersion ||
		context.SkillTreeSHA256 != config.EgoBrowserSkillTreeSHA256 {
		return egoBrowserRuntimeContext{}, errors.New("ego-browser context does not match node configuration")
	}
	if !safeManagedPath(context.WrapperPath) || !safeManagedPath(context.SkillPath) || !safeManagedPath(context.BrokerSocket) {
		return egoBrowserRuntimeContext{}, errors.New("ego-browser context path is invalid")
	}
	if !validOpaqueContext(context.BrokerNonce, 256) {
		return egoBrowserRuntimeContext{}, errors.New("ego-browser broker nonce is invalid")
	}
	if err := validateID(context.TaskSpace, "ego_browser_task_space"); context.TaskSpace != "" && err != nil {
		return egoBrowserRuntimeContext{}, err
	}
	return context, nil
}

func egoBrowserContextRequired(payload map[string]any, config EngineConfig) bool {
	if !config.EgoBrowserEnabled {
		return false
	}
	sessionID, _ := payload["session_id"].(string)
	if sessionID == "" {
		return false
	}
	toolType, _ := payload["tool_type"].(string)
	return toolType == "" || toolType == "claude"
}

func optionalContextString(payload map[string]any, key string) (string, bool) {
	value, present := payload[key]
	if !present {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func validOpaqueContext(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func safeManagedPath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.Contains(value, "..")
}

func validateEgoBrowserArtifacts(wrapperPath string, wrapperVersion string, skillPath string, skillVersion string, skillTreeSHA256 string) error {
	return egobrowserartifact.Verify(egobrowserartifact.RuntimeConfig{
		WrapperPath: wrapperPath, WrapperVersion: wrapperVersion,
		SkillPath: skillPath, SkillVersion: skillVersion, SkillTreeSHA256: skillTreeSHA256,
	})
}

func egoBrowserSocketPath(spec SessionSpec) string {
	return filepath.Join(egoBrowserSandboxSocketRoot, filepath.Base(spec.EgoBrowserBrokerSocket))
}

func egoBrowserEnvironment(spec SessionSpec, sandbox bool) []string {
	if !spec.EgoBrowserEnabled {
		return nil
	}
	socketPath := spec.EgoBrowserBrokerSocket
	wrapperPath := spec.EgoBrowserWrapperPath
	if sandbox {
		socketPath = egoBrowserSocketPath(spec)
		wrapperPath = egoBrowserSandboxWrapperPath
	}
	return []string{
		"EGO_BROWSER_ENABLED=1",
		"EGO_BROWSER_WRAPPER_PATH=" + wrapperPath,
		"EGO_BROWSER_BROKER_SOCKET=" + socketPath,
		"EGO_BROWSER_BROKER_NONCE=" + spec.EgoBrowserBrokerNonce,
		"EGO_BROWSER_PROTOCOL_VERSION=" + spec.EgoBrowserProtocolVersion,
		"EGO_BROWSER_WRAPPER_VERSION=" + spec.EgoBrowserWrapperVersion,
		"EGO_BROWSER_DEFAULT_TASK_SPACE=" + spec.EgoBrowserTaskSpace,
	}
}

func clearEgoBrowserEnvironment(environ []string) []string {
	keys := map[string]struct{}{
		"EGO_BROWSER_ENABLED": {}, "EGO_BROWSER_WRAPPER_PATH": {}, "EGO_BROWSER_BROKER_SOCKET": {},
		"EGO_BROWSER_BROKER_NONCE": {}, "EGO_BROWSER_PROTOCOL_VERSION": {}, "EGO_BROWSER_WRAPPER_VERSION": {},
		"EGO_BROWSER_BINDING_ID": {}, "EGO_BROWSER_DEFAULT_TASK_SPACE": {},
	}
	result := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if _, remove := keys[key]; !remove {
			result = append(result, entry)
		}
	}
	return result
}

func withEgoBrowserEnvironment(environ []string, spec SessionSpec, sandbox bool) []string {
	result := clearEgoBrowserEnvironment(environ)
	for _, entry := range egoBrowserEnvironment(spec, sandbox) {
		key, _, _ := strings.Cut(entry, "=")
		result = replaceEnvironment(result, key, strings.TrimPrefix(entry, key+"="))
	}
	return result
}

func validateSpecEgoBrowserContext(config EngineConfig, spec *SessionSpec, requireNonce bool) error {
	if !spec.EgoBrowserEnabled {
		if spec.EgoBrowserWrapperPath != "" || spec.EgoBrowserBrokerSocket != "" ||
			spec.EgoBrowserProtocolVersion != "" || spec.EgoBrowserWrapperVersion != "" ||
			spec.EgoBrowserSkillPath != "" || spec.EgoBrowserSkillVersion != "" || spec.EgoBrowserSkillTreeSHA256 != "" ||
			spec.EgoBrowserTaskSpace != "" || spec.EgoBrowserBrokerNonce != "" {
			return errors.New("disabled ego-browser spec contains configured values")
		}
		return nil
	}
	if !config.EgoBrowserEnabled || spec.EgoBrowserWrapperPath != config.EgoBrowserWrapperPath ||
		spec.EgoBrowserBrokerSocket != config.EgoBrowserBrokerSocket ||
		spec.EgoBrowserProtocolVersion != config.EgoBrowserProtocolVersion ||
		spec.EgoBrowserWrapperVersion != config.EgoBrowserWrapperVersion ||
		spec.EgoBrowserSkillPath != config.EgoBrowserSkillPath ||
		spec.EgoBrowserSkillVersion != config.EgoBrowserSkillVersion ||
		spec.EgoBrowserSkillTreeSHA256 != config.EgoBrowserSkillTreeSHA256 {
		return errors.New("ego-browser spec does not match node configuration")
	}
	if !safeManagedPath(spec.EgoBrowserWrapperPath) || !safeManagedPath(spec.EgoBrowserSkillPath) || !safeManagedPath(spec.EgoBrowserBrokerSocket) {
		return errors.New("ego-browser spec path is invalid")
	}
	if err := validateID(spec.EgoBrowserTaskSpace, "ego_browser_task_space"); spec.EgoBrowserTaskSpace != "" && err != nil {
		return err
	}
	if requireNonce {
		nonce, ok := os.LookupEnv("EGO_BROWSER_BROKER_NONCE")
		if !ok || !validOpaqueContext(nonce, 256) {
			return errors.New("ego-browser broker nonce is missing")
		}
		spec.EgoBrowserBrokerNonce = nonce
	}
	return nil
}
