package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func stoppedExportFixture(t *testing.T) (Engine, SessionSpec, skillmanager.NodeExportBinding) {
	t.Helper()
	engine, _, spec, launch := preparedManagedLaunch(t)
	sealReconciliationLaunch(t, engine, launch)
	retainExportTermination(t, engine, spec)
	bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	_ = bundle.Close()
	return engine, spec, session.Snapshot.Binding.ExportBinding()
}

func retainExportTermination(t *testing.T, engine Engine, spec SessionSpec) {
	t.Helper()
	bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	_, err = skillmanager.FinalizeWorkTreeWithPolicy(context.Background(), bundle, session.Snapshot.Binding, true,
		skillmanager.CopyPolicy{MinimumFreeBytes: math.MaxUint64})
	if err == nil || !strings.Contains(err.Error(), "insufficient_storage") {
		t.Fatal("fixture did not exhaust frozen-copy reserve", err)
	}
	if _, err := skillmanager.ReadTermination(bundle, session.Snapshot.Binding); err != nil {
		t.Fatal("disk admission failure lost termination evidence", err)
	}
	if _, err := bundle.Lstat("finalization"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed freeze created finalization", err)
	}
}

func TestHelperFinalizationStoppedExportAfterCopyReserveFailure(t *testing.T) {
	for _, kind := range []string{"missing_unit", "exited_unit", "runtime_quota", "previous_boot", "missing_termination", "previous_boot_without_termination"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, binding := stoppedExportFixture(t)
			var actions string
			if kind == "exited_unit" {
				engine.config.SystemctlPath, actions = reconciliationSystemctl(t, spec, "clean", strings.Repeat("a", 32))
			}
			if strings.HasPrefix(kind, "previous_boot") {
				engine, _, spec = previousBootSkillFixture(t, "started")
				retainExportTermination(t, engine, spec)
				bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				binding = session.Snapshot.Binding.ExportBinding()
				_ = bundle.Close()
			}
			bundlePath := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
			if kind == "runtime_quota" {
				// Keep a complete original baseline but model a smaller retained runtime quota.
				path := filepath.Join(bundlePath, "snapshot.json")
				var snapshot skillmanager.PreparedSnapshot
				data, err := os.ReadFile(path)
				if err != nil || json.Unmarshal(data, &snapshot) != nil {
					t.Fatal("invalid fixture", err)
				}
				snapshot.Capture.DirectoryBytes = 8
				writeRebootFixtureJSON(t, path, snapshot)
				if err := os.WriteFile(filepath.Join(bundlePath, "work", "learned"), []byte("extra runtime learning"), 0o600); err != nil {
					t.Fatal(err)
				}
				bundle, session, err := engine.retainedNativeSkillSession(spec.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = skillmanager.FinalizeWorkTree(context.Background(), bundle, session.Snapshot.Binding, true)
				_ = bundle.Close()
				if err == nil || !strings.Contains(err.Error(), "quota_exceeded") {
					t.Fatal("runtime quota did not prevent freezing", err)
				}
			}
			withoutTermination := kind == "missing_termination" || kind == "previous_boot_without_termination"
			if withoutTermination {
				mustExportFixture(t, os.Remove(filepath.Join(bundlePath, "termination.json")))
			}
			before, err := os.ReadFile(filepath.Join(bundlePath, "termination.json"))
			if err != nil && !(withoutTermination && errors.Is(err, os.ErrNotExist)) {
				t.Fatal(err)
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			var output bytes.Buffer
			checks, forced := 0, 0
			err = client.StreamStoppedSkillExport(context.Background(), "recover", binding, &output, func(header skillexport.Header, force bool) error {
				checks++
				if force {
					forced++
				}
				if header.Binding != binding || !header.Unclean {
					return skillexport.ErrUnavailable
				}
				return nil
			})
			if err != nil || checks < 4 || forced != 2 || !bytes.Contains(output.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("stopped work could not be exported", err, output.Len(), checks)
			}
			var verified bytes.Buffer
			if err := skillexport.RelaySnapshot(context.Background(), bytes.NewReader(output.Bytes()), &verified, func(skillexport.Header, bool) error { return nil }); err != nil || !bytes.Equal(verified.Bytes(), output.Bytes()) {
				t.Fatal("export is not a complete verified bundle", err)
			}
			if _, err := os.Lstat(filepath.Join(bundlePath, "finalization")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("export froze work", err)
			}
			after, err := os.ReadFile(filepath.Join(bundlePath, "termination.json"))
			if (err != nil && !(withoutTermination && errors.Is(err, os.ErrNotExist))) || !bytes.Equal(before, after) {
				t.Fatal("export changed termination", err)
			}
			if actions != "" {
				if _, err := os.Lstat(actions); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("export issued a runtime mutation", err)
				}
			}
		})
	}
}

func TestHelperFinalizationStoppedExportRefusesUncertainSources(t *testing.T) {
	for _, kind := range []string{"foreign", "running", "replacement", "populated", "missing_termination", "linked_termination", "missing_draft", "corrupt_launch", "existing_finalization", "linked_finalization", "linked_work", "special_file"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, binding := stoppedExportFixture(t)
			bundle := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
			switch kind {
			case "foreign":
				binding.AccountID = binding.UserID
			case "running", "replacement":
				state, invocation := "running", strings.Repeat("a", 32)
				if kind == "replacement" {
					state, invocation = "clean", strings.Repeat("b", 32)
				}
				engine.config.SystemctlPath, _ = reconciliationSystemctl(t, spec, state, invocation)
			case "populated":
				group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
				mustExportFixture(t, os.MkdirAll(group, 0o700))
				mustExportFixture(t, os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0o600))
			case "missing_termination", "linked_termination":
				if kind == "missing_termination" {
					mustExportFixture(t, os.Remove(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json")))
				}
				mustExportFixture(t, os.Remove(filepath.Join(bundle, "termination.json")))
				if kind == "linked_termination" {
					mustExportFixture(t, os.Symlink("missing", filepath.Join(bundle, "termination.json")))
				}
			case "missing_draft":
				mustExportFixture(t, os.Remove(filepath.Join(engine.config.SkillStateRoot, "spec-draft-"+spec.SessionID+".json")))
			case "corrupt_launch":
				mustExportFixture(t, os.WriteFile(filepath.Join(engine.config.SkillStateRoot, "session-launch-"+spec.SessionID+".json"), []byte("{}"), 0o600))
			case "existing_finalization":
				mustExportFixture(t, os.Mkdir(filepath.Join(bundle, "finalization"), 0o700))
			case "linked_finalization":
				mustExportFixture(t, os.Symlink("missing", filepath.Join(bundle, "finalization")))
			case "linked_work":
				mustExportFixture(t, os.Rename(filepath.Join(bundle, "work"), filepath.Join(bundle, "original-work")))
				mustExportFixture(t, os.Symlink("original-work", filepath.Join(bundle, "work")))
			case "special_file":
				mustExportFixture(t, os.Symlink("/etc/passwd", filepath.Join(bundle, "work", "escape")))
			}
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			var output bytes.Buffer
			err := client.StreamStoppedSkillExport(context.Background(), "deny", binding, &output, func(skillexport.Header, bool) error { return nil })
			if err == nil || output.Len() != 0 {
				t.Fatal("unproven work disclosed", err, output.Len())
			}
		})
	}
}

func TestHelperFinalizationStoppedExportRechecksWholeTreeAndWriters(t *testing.T) {
	for _, kind := range []string{"new_file", "changed_bytes", "replaced_work", "writer_returned", "changed_termination", "frozen_during_read"} {
		t.Run(kind, func(t *testing.T) {
			engine, spec, binding := stoppedExportFixture(t)
			bundle := filepath.Join(engine.config.SkillStateRoot, "session-"+spec.SessionID)
			writer := &exportMutationWriter{mutate: func() {
				switch kind {
				case "new_file", "changed_bytes":
					name := "new"
					if kind == "changed_bytes" {
						name = "notes"
					}
					mustExportFixture(t, os.WriteFile(filepath.Join(bundle, "work", name), []byte("modified"), 0o600))
				case "replaced_work":
					mustExportFixture(t, os.Rename(filepath.Join(bundle, "work"), filepath.Join(bundle, "old-work")))
					mustExportFixture(t, os.Mkdir(filepath.Join(bundle, "work"), 0o700))
				case "writer_returned":
					group := filepath.Join(engine.config.CgroupRoot, "system.slice", spec.UnitName)
					mustExportFixture(t, os.MkdirAll(group, 0o700))
					mustExportFixture(t, os.WriteFile(filepath.Join(group, "cgroup.events"), []byte("populated 1\n"), 0o600))
				case "changed_termination":
					mustExportFixture(t, os.WriteFile(filepath.Join(bundle, "termination.json"), []byte("{}"), 0o600))
				case "frozen_during_read":
					mustExportFixture(t, os.Mkdir(filepath.Join(bundle, "finalization"), 0o700))
				}
			}}
			if err := engine.streamStoppedExport(context.Background(), writer, binding); err == nil || bytes.Contains(writer.Bytes(), []byte(`"complete":true`)) {
				t.Fatal("changing source received successful completion", err)
			}
		})
	}
}

type exportMutationWriter struct {
	bytes.Buffer
	mutate func()
}

func (w *exportMutationWriter) Write(data []byte) (int, error) {
	if w.mutate != nil {
		mutate := w.mutate
		w.mutate = nil
		mutate()
	}
	return w.Buffer.Write(data)
}

// io.WriteString must take the same mutation hook as object writes.
func (w *exportMutationWriter) WriteString(data string) (int, error) { return w.Write([]byte(data)) }

func mustExportFixture(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestHelperFinalizationStoppedExportCancellationReleasesLockWait(t *testing.T) {
	engine, _, binding := stoppedExportFixture(t)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	server.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err := client.StreamStoppedSkillExport(ctx, "cancel", binding, io.Discard, func(skillexport.Header, bool) error { return nil })
	cancel()
	server.mu.Unlock()
	if err == nil {
		t.Fatal("cancelled lock wait succeeded")
	}
	if err := client.StreamStoppedSkillExport(context.Background(), "retry", binding, io.Discard, func(skillexport.Header, bool) error { return nil }); err != nil {
		t.Fatal("cancelled handler leaked lock", err)
	}
}
