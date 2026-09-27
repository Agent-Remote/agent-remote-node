package runtime

import (
	"slices"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

func managedSkillCapabilities(backends []string, value any) map[string]api.SkillManagerCapability {
	result := map[string]api.SkillManagerCapability{}
	if !slices.Contains(backends, "native") {
		return result
	}
	reports, _ := value.(map[string]any)
	report, _ := reports["native"].(map[string]any)
	for _, field := range []string{"protocol_version", "manifest_version", "deployment_protocol_version"} {
		version, ok := report[field].(float64)
		if !ok || version != 1 {
			return result
		}
	}
	for _, field := range []string{"writable_copies", "finalization", "recovery"} {
		if report[field] != true {
			return result
		}
	}
	result["native"] = api.SkillManagerCapability{
		ProtocolVersion: 1, ManifestVersion: 1, DeploymentProtocolVersion: 1,
		WritableCopies: true, Finalization: true, Recovery: true,
	}
	return result
}
