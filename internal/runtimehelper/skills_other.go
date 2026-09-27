//go:build !linux

package runtimehelper

import (
	"context"
	"errors"
)

func (e Engine) managedNativeStopAuthority(_ context.Context, spec SessionSpec) (SessionSpec, string, error) {
	return spec, "", nil
}

func (e Engine) recoverPreviousBootSkillStop(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

func (e Engine) stopRetainedNativeSkillSession(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

func (e Engine) finalizeNativeSkillSession(_ context.Context, spec SessionSpec, _ nativeTermination) (map[string]any, error) {
	if spec.SkillSnapshotID != "" {
		return nil, errors.New("managed skill finalization requires the Linux helper")
	}
	return nil, nil
}

func (e Engine) mountNativeSkills(_ context.Context, spec SessionSpec) error {
	if spec.SkillSnapshotID != "" {
		return errors.New("managed skill mounts require the Linux helper")
	}
	return nil
}

func (e Engine) cleanupNativeSkillMount(spec SessionSpec) error {
	return e.mountNativeSkills(context.Background(), spec)
}
