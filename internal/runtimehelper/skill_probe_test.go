package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillProbeDoesNotCreateStoreWithoutAdmission(t *testing.T) {
	for _, boundary := range []string{"disabled", "native-unavailable", "binary-missing", "cancelled"} {
		t.Run(boundary, func(t *testing.T) {
			store := filepath.Join(t.TempDir(), "must-not-create")
			engine := NewEngine(EngineConfig{SkillManagerEnabled: true, SkillStateRoot: store, RuntimeBinaryPath: "/bin/sh"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			native := true
			switch boundary {
			case "disabled":
				engine.config.SkillManagerEnabled = false
			case "native-unavailable":
				native = false
			case "binary-missing":
				engine.config.RuntimeBinaryPath = filepath.Join(t.TempDir(), "absent")
			case "cancelled":
				cancel()
			}
			reports, checks := engine.probeManagedSkills(ctx, native)
			if len(reports) != 0 || checks["state_storage"] {
				t.Fatalf("premature capability: %#v", reports)
			}
			if _, err := os.Lstat(store); !os.IsNotExist(err) {
				t.Fatal("probe touched disabled storage")
			}
		})
	}
}
