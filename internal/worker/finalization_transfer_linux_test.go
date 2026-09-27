package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// The fixture seals already-quiescent files. This tests the actual background transfer boundary,
// not process-exit proof, systemd launch, or the Server's real database publication algorithm.
// It deliberately lacks managed launch authority, so transfer completes while reclamation stays pending.
func TestFinalizationBackgroundTransfersRealHelperObjectsAfterWorkRemoval(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root Linux Helper")
	}
	for _, unclean := range []bool{false, true} {
		name, status := "clean", "published"
		if unclean {
			name, status = "unclean", "detached"
		}
		t.Run(name, func(t *testing.T) {
			f := newFinalizationFixture(t, unclean, status)
			root := t.TempDir()
			config := runtimehelper.EngineConfig{NodeID: f.capture.Binding.NodeID, SkillStateRoot: filepath.Join(root, "skills"), StateRoot: filepath.Join(root, "runtime"), WorkspaceRoot: filepath.Join(root, "workspaces"), AccountRoot: filepath.Join(root, "accounts"), BrowserRoot: filepath.Join(root, "browsers"), EgoBrowserBrokerRoot: filepath.Join(root, "broker")}
			boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
			if err != nil {
				t.Fatal(err)
			}
			config.CgroupRoot = filepath.Join(root, "cgroups")
			if err := os.Mkdir(config.CgroupRoot, 0700); err != nil {
				t.Fatal(err)
			}
			config.SystemctlPath = filepath.Join(root, "systemctl")
			config.IPPath = filepath.Join(root, "ip")
			if err := os.WriteFile(config.SystemctlPath, []byte("#!/bin/sh\n[ \"$1\" = show ] || exit 1\nprintf 'LoadState=not-found\\nActiveState=inactive\\nControlGroup=\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config.IPPath, []byte("#!/bin/sh\n[ \"$1 $2\" = 'netns list' ]\n"), 0700); err != nil {
				t.Fatal(err)
			}
			store, err := skillmanager.OpenStateStore(config.SkillStateRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			options := skillmanager.MaterializeOptions{UID: 65534, GID: 65534, Policy: skillmanager.CopyPolicy{DirectoryBytes: 1 << 20, Entries: 100}}
			unitDigest := sha256.Sum256([]byte(f.capture.Binding.SessionID))
			receipt := skillmanager.SessionSnapshot{Snapshot: skillmanager.PreparedSnapshot{Version: 1, Binding: f.capture.Binding, Capture: skillmanager.CaptureOptions{DirectoryBytes: options.Policy.DirectoryBytes, Entries: options.Policy.Entries}}, Runtime: skillmanager.RuntimeBinding{Backend: "native", ResourceID: "agent-remote-session-" + hex.EncodeToString(unitDigest[:])[:12] + ".service", BootID: strings.TrimSpace(string(boot)), UID: 65534, GID: 65534}}
			if err := skillmanager.PrepareSessionSnapshot(context.Background(), store, f.manifest, func(context.Context, string) (io.ReadCloser, error) { return os.Open(f.object) }, receipt, options); err != nil {
				t.Fatal(err)
			}
			bundle, _, err := skillmanager.OpenSessionSnapshot(store, f.capture.Binding.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			actual, err := skillmanager.FinalizeWorkTree(context.Background(), bundle, f.capture.Binding, unclean)
			if err != nil || actual != f.capture {
				t.Fatal("fixture did not seal original capture", err)
			}
			if err := bundle.RemoveAll("work"); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(root, "helper.sock")
			engine := runtimehelper.NewEngine(config)
			server := runtimehelper.NewServer(socket, -1, 0, engine)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- server.Serve(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(time.Second):
					t.Error("Helper did not stop")
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(socket); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Helper socket unavailable")
				}
				time.Sleep(time.Millisecond)
			}
			helper := runtimehelper.NewClient(socket)
			f.fault = "complete"
			if _, err := recoverFinalizationPage(ctx, f.client, helper, f.journal, config.NodeID, ""); err == nil {
				t.Fatal("lost completion treated as acknowledged")
			}
			f.reopen()
			if next, err := recoverFinalizationPage(ctx, f.client, helper, f.journal, config.NodeID, ""); !errors.Is(err, errFinalizationPending) || next != "" {
				t.Fatal("legacy input did not retain pending reclamation", err, next)
			}
			saved, err := f.journal.load(f.capture)
			if err != nil || saved.record.Publication == nil || saved.record.Publication.Status != status {
				t.Fatal("lost original publication decision", err)
			}
			retained, err := skillmanager.ReadFinalization(bundle, f.capture.Binding)
			if err != nil || !skillmanager.SameFinalizationInput(retained, f.capture) || retained.State != status {
				t.Fatal("worker transfer failed to acknowledge original retention", err)
			}
			object, _, err := skillmanager.OpenFinalizationObject(bundle, f.capture, f.manifest.Entries[0].SHA256)
			if err != nil {
				t.Fatal("publication removed retained bytes", err)
			}
			_ = object.Close()
			if err := os.Remove(f.path); err != nil {
				t.Fatal(err)
			}
			f.reopen()
			if _, err := recoverFinalizationPage(ctx, f.client, helper, f.journal, config.NodeID, ""); !errors.Is(err, errFinalizationPending) {
				t.Fatal("legacy input gained reclamation authority after ledger reconstruction", err)
			}
			if saved, err := f.journal.load(f.capture); err != nil || saved == nil || saved.record.Publication == nil {
				t.Fatal("lost worker ledger could not reconstruct original terminal receipts", err)
			}
			if _, err := bundle.Lstat("finalization/reclamation.json"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("missing original managed launch authority allowed marking", err)
			}
		})
	}
}
