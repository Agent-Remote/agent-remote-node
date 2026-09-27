package runtime

import "testing"

func TestSkillCapabilitiesRequireCompleteHelperReportAndAllowedNative(t *testing.T) {
	for _, boundary := range []string{"valid", "missing", "old-helper", "boolean-version", "string-version", "future-version", "no-finalization", "no-recovery", "docker-only", "disallowed-native"} {
		t.Run(boundary, func(t *testing.T) {
			report := map[string]any{
				"protocol_version": 1, "manifest_version": 1, "deployment_protocol_version": 1,
				"writable_copies": true, "finalization": true, "recovery": true,
			}
			reports := map[string]any{"native": report, "docker_sandbox": report}
			allowed := []string{"native", "docker_sandbox"}
			switch boundary {
			case "missing":
				delete(report, "manifest_version")
			case "old-helper":
				reports = nil
			case "boolean-version":
				report["protocol_version"] = true
			case "string-version":
				report["manifest_version"] = "1"
			case "future-version":
				report["deployment_protocol_version"] = 2
			case "no-finalization":
				report["finalization"] = false
			case "no-recovery":
				report["recovery"] = 1
			case "docker-only":
				delete(reports, "native")
			case "disallowed-native":
				allowed = []string{"docker_sandbox"}
			}
			socket, done := serveRuntimeProbe(t, map[string]any{
				"backends": []string{"native", "docker_sandbox"}, "skill_manager": reports,
				"skill_manager_checks": map[string]bool{"configured_enabled": true},
			})
			result := probeCapabilities(allowed, socket, "")
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if boundary == "valid" {
				if len(result.SkillManager) != 1 || !result.SkillManager["native"].Recovery {
					t.Fatalf("verified Native lifecycle missing: %#v", result.SkillManager)
				}
			} else if len(result.SkillManager) != 0 {
				t.Fatalf("unsupported report accepted: %#v", result.SkillManager)
			}
			if !result.SkillManagerChecks["configured_enabled"] {
				t.Fatal("probe diagnostics lost")
			}
		})
	}
}
