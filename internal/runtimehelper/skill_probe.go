package runtimehelper

import "context"

func (e Engine) probeManagedSkills(ctx context.Context, nativeOK bool) (map[string]any, map[string]bool) {
	checks := map[string]bool{
		"configured_enabled": e.config.SkillManagerEnabled,
		"native_available":   nativeOK,
		"runtime_binary":     executableExists(e.config.RuntimeBinaryPath),
		"state_storage":      false,
	}
	reports := map[string]any{}
	if !checks["configured_enabled"] || !nativeOK || !checks["runtime_binary"] || ctx.Err() != nil {
		return reports, checks
	}
	checks["state_storage"] = e.probeSkillStorage() == nil
	if checks["state_storage"] && ctx.Err() == nil {
		reports["native"] = map[string]any{
			"protocol_version": 1, "manifest_version": 1, "deployment_protocol_version": 1,
			"writable_copies": true, "finalization": true, "recovery": true,
		}
	}
	return reports, checks
}
