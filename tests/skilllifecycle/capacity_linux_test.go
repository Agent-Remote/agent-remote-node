package skilllifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// TestNativePipelineCapacity runs actual daemons and a deterministic tool, without model inference.
func TestNativePipelineCapacity(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_SKILL_PIPELINE_CAPACITY") != "1" {
		t.Skip("requires disposable Server/PostgreSQL and explicit default-capacity acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	f := readFixture(t)
	byteCapacity := os.Getenv("AGENT_REMOTE_RUN_SKILL_PIPELINE_BYTES") == "1"
	combined := os.Getenv("AGENT_REMOTE_RUN_SKILL_PIPELINE_COMBINED") == "1"
	if combined && !byteCapacity {
		t.Fatal("combined pipeline capacity requires the byte workload")
	}
	writeArgs, readArgs := []string{"--capacity-write"}, []string{"--capacity-read"}
	attachTimeout := 3 * time.Minute
	if byteCapacity {
		writeArgs, readArgs = append(writeArgs, "--capacity-bytes"), append(readArgs, "--capacity-bytes")
		attachTimeout = 3 * time.Hour
		if combined {
			writeArgs, readArgs = append(writeArgs, "--capacity-entries"), append(readArgs, "--capacity-entries")
		}
	}
	// Large snapshot reservation must not inherit the small smoke fixture's 20-second limit.
	f.HTTPTimeout = 10 * time.Minute
	prepareDaemons(t, ctx, f)
	await(t, ctx, "ordinary takeover and deployment", func() bool {
		result, err := f.request(ctx, "GET", "/api/v1/skills/operations/"+f.OperationID, nil)
		return err == nil && result.Status == "ready"
	})
	first := createSession(t, ctx, f, writeArgs)
	runAttachedWithin(t, ctx, first, false, attachTimeout)
	written, err := os.ReadFile(filepath.Join(workspacePath(f), "capacity-witness"))
	if err != nil || string(written) != "written" {
		status, _ := os.ReadFile(filepath.Join(workspacePath(f), "capacity-status"))
		if regexp.MustCompile(`^(start|instructions|files|bytes|cardinality):[A-Za-z]{1,40}$`).Match(status) {
			t.Log("capacity tool failure: " + string(status))
		}
		t.Fatal("capacity tool did not complete its full workload")
	}
	firstCapture := awaitCapacityPublication(t, ctx, first)
	if firstCapture.Unclean || firstCapture.State != "published" {
		t.Fatal("default-capacity original input was not cleanly published")
	}
	manifestData, err := os.ReadFile(filepath.Join(skillRoot, "session-"+first, "finalization/manifest.json"))
	var manifest skillmanager.Manifest
	if err != nil || json.Unmarshal(manifestData, &manifest) != nil {
		t.Fatal("default-capacity capture lost original manifest")
	}
	checkPipelineCapacityManifest(t, manifest, byteCapacity, combined)
	if byteCapacity {
		// Reuse only space released by production's fresh Server-authorized reclamation.
		awaitCapacityReclamation(t, ctx, f, first)
	}
	command(t, ctx, "systemctl", "stop", workerUnit, helperUnit)
	startDaemons(t, ctx)
	response, err := f.request(ctx, "POST", "/api/v1/sessions", map[string]any{
		"tool_type": "claude", "tool_account_id": f.AccountID, "workspace_id": f.WorkspaceID,
		"project_key": "skill-lifecycle", "argv": readArgs,
	})
	if err != nil || response.Data.ID == "" || response.Data.ID == first {
		t.Fatal("independent capacity session admission failed", err)
	}
	second := response.Data.ID
	awaitCapacity(t, ctx, "materialization", func() bool {
		state, err := f.request(ctx, "GET", "/api/v1/sessions/"+second, nil)
		if err == nil && (state.Data.Status == "failed" || state.Data.Status == "stopped") {
			t.Fatal("capacity session terminated before attach")
		}
		return err == nil && state.Data.Status == "running"
	})
	runAttachedWithin(t, ctx, second, false, attachTimeout)
	witness, err := os.ReadFile(filepath.Join(workspacePath(f), "capacity-witness"))
	if err != nil || string(witness) != "inherited" {
		t.Fatal("independent runtime did not verify every inherited capacity object")
	}
	secondCapture := awaitCapacityPublication(t, ctx, second)
	if secondCapture.Unclean || secondCapture.State != "published" || secondCapture.TreeDigest != firstCapture.TreeDigest || secondCapture.Binding.SnapshotID == firstCapture.Binding.SnapshotID {
		t.Fatal("independent session changed capacity content or reused original identity")
	}
	if byteCapacity {
		awaitCapacityReclamation(t, ctx, f, second)
	}
	t.Log("PIPELINE_CAPACITY_PUBLISHED_AND_INHERITED")
}

func checkPipelineCapacityManifest(t *testing.T, manifest skillmanager.Manifest, byteCapacity, combined bool) {
	t.Helper()
	entries, files := 100_000, 99_999
	if byteCapacity {
		entries, files = 30, 20
		if combined {
			entries, files = 100_000, 99_990
		}
	}
	objects := make(map[string]bool)
	var total int64
	count := 0
	for _, entry := range manifest.Entries {
		if entry.Kind == "file" {
			count++
			objects[entry.SHA256] = true
			total += entry.Size
		}
	}
	if len(manifest.Entries) != entries || count != files || len(objects) != files || (byteCapacity && total != 10<<30) {
		t.Fatal("default-capacity capture lost original entries, distinct objects or bytes")
	}
}

func awaitCapacityReclamation(t *testing.T, ctx context.Context, f lifecycleFixture, session string) {
	t.Helper()
	path := filepath.Join(skillRoot, "session-"+session, "finalization/reclaimed.json")
	awaitCapacity(t, ctx, "reclamation", func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	awaitReclaimed(t, ctx, f, session)
}

func awaitCapacityPublication(t *testing.T, ctx context.Context, session string) skillmanager.FinalizationRecord {
	t.Helper()
	var record skillmanager.FinalizationRecord
	path := filepath.Join(skillRoot, "session-"+session, "finalization/record.json")
	reported := time.Time{}
	awaitCapacity(t, ctx, "finalization", func() bool {
		data, err := os.ReadFile(path)
		if time.Since(reported) >= time.Minute {
			observeCapacityFinalization(t, data, err)
			reported = time.Now()
		}
		if err != nil || json.Unmarshal(data, &record) != nil {
			return false
		}
		if record.Unclean || record.State == "detached" || record.State == "conflicted" {
			t.Fatal("capacity finalization did not preserve a clean publishable input")
		}
		return record.State == "published"
	})
	return record
}

func awaitCapacity(t *testing.T, parent context.Context, phase string, check func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 3*time.Hour)
	defer cancel()
	started, reported := time.Now(), time.Now()
	for !check() {
		if time.Since(reported) >= time.Minute {
			t.Logf("capacity_phase=%s elapsed_seconds=%.1f", phase, time.Since(started).Seconds())
			reported = time.Now()
		}
		select {
		case <-ctx.Done():
			t.Fatal("default-capacity phase timed out: " + phase)
		case <-time.After(time.Second):
		}
	}
	t.Logf("capacity_phase=%s complete_seconds=%.1f", phase, time.Since(started).Seconds())
}
