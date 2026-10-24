package runtime

import (
	"encoding/json"
	"slices"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

func sessionLifecycleCapabilities(backends []string, value any) map[string]api.SessionLifecycleCapability {
	result := map[string]api.SessionLifecycleCapability{}
	reports, _ := value.(map[string]any)
	for _, backend := range []string{"native", "docker_sandbox"} {
		if !slices.Contains(backends, backend) {
			continue
		}
		report, _ := reports[backend].(map[string]any)
		if len(report) != 2 || report["supported"] != true {
			continue
		}
		version, ok := report["protocol_version"].(json.Number)
		if !ok || version != "1" {
			continue
		}
		result[backend] = api.SessionLifecycleCapability{Supported: true, ProtocolVersion: 1}
	}
	return result
}
