package runtimehelper

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func TestHelperFinalizationStoppedExportPhysicalENOSPC(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_TEST_EXPORT_ENOSPC") != "1" {
		t.Skip("requires the isolated bounded tmpfs export runner")
	}
	var fs unix.Statfs_t
	const volume = "/skill-export-full"
	if err := unix.Statfs(volume, &fs); err != nil || fs.Type != unix.TMPFS_MAGIC || fs.Bsize <= 0 || fs.Blocks > (64<<20)/uint64(fs.Bsize) {
		t.Fatal("disk-full proof requires an isolated tmpfs no larger than 64 MiB")
	}
	engine, _, spec, launch := preparedManagedLaunchWithConfig(t, func(engine *Engine, input *ManagedSessionSpecRequest) {
		original, err := engine.openExistingSkillStateRoot()
		if err != nil {
			t.Fatal(err)
		}
		fence, err := skillmanager.ReadAccountFence(original, input.Snapshot.NodeID, input.Snapshot.UserID, input.Snapshot.AccountID)
		_ = original.Close()
		if err != nil {
			t.Fatal(err)
		}
		root, err := os.MkdirTemp(volume, "private-skills-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		// Only the private skill volume is small. Runtime workspace ACLs stay on the normal
		// test filesystem; Docker Desktop tmpfs need not support POSIX ACLs.
		engine.config.SkillStateRoot = root
		engine.config.SkillStatePolicy.MinimumFreeBytes = 1
		store, err := engine.openExistingSkillStateRoot()
		if err != nil {
			t.Fatal(err)
		}
		_, err = skillmanager.CloseAccountImports(store, fence)
		_ = store.Close()
		mustExportFixture(t, err)
	})
	sealReconciliationLaunch(t, engine, launch)
	bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	fillerPath := filepath.Join(engine.config.SkillStateRoot, "fill-only-this-test-volume")
	filler, err := os.OpenFile(fillerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = filler.Close(); _ = os.Remove(fillerPath) }()
	block := bytes.Repeat([]byte{1}, 64<<10)
	for written := 0; written <= 64<<20; written += len(block) {
		if _, err = filler.Write(block); err != nil {
			break
		}
	}
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("isolated filesystem was not physically exhausted", err)
	}
	_, err = skillmanager.FinalizeWorkTree(context.Background(), bundle, session.Snapshot.Binding, false)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("full volume did not prevent termination retention", err)
	}
	for _, name := range []string{"termination.json", "finalization"} {
		if _, err := bundle.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failure unexpectedly retained finalization authority", name, err)
		}
	}
	var output bytes.Buffer
	err = client.StreamStoppedSkillExport(context.Background(), "enospc-recovery", session.Snapshot.Binding.ExportBinding(), &output, func(header skillexport.Header, _ bool) error {
		if !header.Unclean || len(header.Manifest.Entries) != 1 ||
			skillmanager.VerifyContent(header.Manifest.Entries[0], []byte("original")) != nil {
			return skillexport.ErrUnavailable
		}
		return nil
	})
	if err != nil || !bytes.Contains(output.Bytes(), []byte(`"complete":true`)) {
		t.Fatal("full-volume recovery could not read complete original content", err)
	}
	for _, name := range []string{"termination.json", "finalization"} {
		if _, err := bundle.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("read-only recovery created durable authority", name, err)
		}
	}
	if _, err := filler.Write([]byte{1}); !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("export recovered by reclaiming space", err)
	}
}
