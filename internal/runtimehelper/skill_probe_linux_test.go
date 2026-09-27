package runtimehelper

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestSkillProbeRequiresPrivateWritablePersistentVolume(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires privileged Linux state store")
	}
	root := t.TempDir()
	policy := skillmanager.DefaultStatePolicy()
	engine := NewEngine(EngineConfig{
		SkillManagerEnabled: true, SkillStateRoot: filepath.Join(root, "skills"),
		RuntimeBinaryPath: "/bin/sh", SkillStatePolicy: policy,
	})
	reports, checks := engine.probeManagedSkills(context.Background(), true)
	if len(reports) != 1 || reports["native"] == nil || !checks["state_storage"] {
		t.Fatalf("private Native storage not accepted: %#v %#v", reports, checks)
	}
	entries, err := os.ReadDir(engine.config.SkillStateRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("probe left files: %v %v", entries, err)
	}
	if err := os.Chmod(engine.config.SkillStateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	reports, checks = engine.probeManagedSkills(context.Background(), true)
	if len(reports) != 0 || checks["state_storage"] {
		t.Fatal("unsafe state store advertised")
	}
	if err := os.Chmod(engine.config.SkillStateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	engine.config.SkillStatePolicy.MinimumFreeBytes = ^uint64(0)
	reports, checks = engine.probeManagedSkills(context.Background(), true)
	if len(reports) != 0 || checks["state_storage"] {
		t.Fatal("exhausted reserve advertised")
	}
}
