package runtimehelper

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyRuntimeAdmissionDoesNotCreateUnusedSkillStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "unused-skills")
	engine := NewEngine(EngineConfig{SkillStateRoot: root})
	if err := engine.requireLegacyAccountRuntime("user_1", "account_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy admission created a private store: %v", err)
	}
}

func TestLegacyRuntimeAdmissionAllowsConfiguredRootAlias(t *testing.T) {
	root := t.TempDir()
	accounts := filepath.Join(root, "accounts")
	if err := os.Mkdir(accounts, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "configured-root")
	if err := os.Symlink(accounts, alias); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(EngineConfig{AccountRoot: alias, SkillStateRoot: filepath.Join(root, "unused-skills")})
	if err := engine.requireLegacyAccountRuntime("user_1", "account_1"); err != nil {
		t.Fatal("configured root alias was confused with an account alias", err)
	}
}
