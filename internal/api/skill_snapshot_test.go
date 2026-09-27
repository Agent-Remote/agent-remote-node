package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func snapshotTestView(t *testing.T, content []byte) (SkillSnapshot, skillmanager.Entry) {
	t.Helper()
	account := takeoverTestBinding()
	entry := takeoverTestFile(content)
	entry.Path = "learning/SKILL.md"
	entry.ContentKind = "text"
	manifest := skillmanager.Manifest{Version: 1, Entries: []skillmanager.Entry{
		{Path: "learning", Kind: "directory", Mode: 0o755}, entry,
	}}
	digest, err := skillmanager.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return SkillSnapshot{
		SkillSnapshotIdentity: SkillSnapshotIdentity{
			SnapshotID: takeoverTestUpload, TaskID: account.TaskID, NodeID: account.NodeID,
			UserID: account.UserID, AccountID: account.AccountID, SessionID: takeoverTestHelper, RuntimeBackend: "native",
		},
		LibraryGeneration: 9007199254740993, DirectoryEpoch: 9007199254740995,
		TreeDigest: digest, Manifest: manifest,
		Items: []SkillSnapshotItem{{
			StateID: takeoverTestCheckpoint, EntryName: "learning", StateEpoch: 9007199254740997,
			CheckpointID: takeoverTestUpload,
			Resolution: SkillSnapshotResolution{Enabled: true, Eligible: true, Included: true,
				RevisionID: takeoverTestHelper, EnabledSource: "account", RevisionSource: "tool"},
		}},
		SystemReleases: map[string]json.RawMessage{"ego-browser": json.RawMessage(`{"version":"pinned"}`)},
	}, entry
}

func snapshotTestEnvelope(view SkillSnapshot) skillEnvelope[SkillSnapshot] {
	committed := false
	return skillEnvelope[SkillSnapshot]{SchemaVersion: 1, Status: "ready", Committed: &committed, Data: view}
}

func TestSkillSnapshotOriginalBindingAndInt64Precision(t *testing.T) {
	view, _ := snapshotTestView(t, []byte("# private instructions\n"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/node/skill-snapshots/"+view.SnapshotID ||
			r.URL.Query().Get("task_id") != view.TaskID || r.Header.Get("Authorization") != "Bearer node-token" {
			t.Error("snapshot request lost exact authentication or binding")
		}
		_ = json.NewEncoder(w).Encode(snapshotTestEnvelope(view))
	}))
	defer server.Close()
	actual, err := NewClient(server.URL, "node-token").GetSkillSnapshot(context.Background(), view.SkillSnapshotIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if actual.SkillSnapshotIdentity != view.SkillSnapshotIdentity || actual.TreeDigest != view.TreeDigest ||
		actual.LibraryGeneration != view.LibraryGeneration || actual.DirectoryEpoch != view.DirectoryEpoch || actual.Items[0].StateEpoch != view.Items[0].StateEpoch {
		t.Fatal("snapshot identity or integer precision changed")
	}
}

func TestSkillSnapshotRejectsChangedIdentitiesAndInconsistentMembers(t *testing.T) {
	for name, change := range map[string]func(*SkillSnapshot){
		"snapshot":        func(v *SkillSnapshot) { v.SnapshotID = takeoverTestCheckpoint },
		"task":            func(v *SkillSnapshot) { v.TaskID = takeoverTestCheckpoint },
		"node":            func(v *SkillSnapshot) { v.NodeID = takeoverTestCheckpoint },
		"user":            func(v *SkillSnapshot) { v.UserID = takeoverTestCheckpoint },
		"account":         func(v *SkillSnapshot) { v.AccountID = takeoverTestCheckpoint },
		"session":         func(v *SkillSnapshot) { v.SessionID = takeoverTestCheckpoint },
		"backend":         func(v *SkillSnapshot) { v.RuntimeBackend = "docker_sandbox" },
		"generation":      func(v *SkillSnapshot) { v.LibraryGeneration = -1 },
		"epoch":           func(v *SkillSnapshot) { v.DirectoryEpoch = 0 },
		"tree":            func(v *SkillSnapshot) { v.TreeDigest = strings.Repeat("0", 64) },
		"member epoch":    func(v *SkillSnapshot) { v.Items[0].StateEpoch = 0 },
		"member root":     func(v *SkillSnapshot) { v.Items[0].EntryName = "absent" },
		"member identity": func(v *SkillSnapshot) { v.Items[0].CheckpointID = "invalid" },
		"disabled":        func(v *SkillSnapshot) { v.Items[0].Resolution.Enabled = false },
		"excluded":        func(v *SkillSnapshot) { v.Items[0].Resolution.Included = false },
		"reason":          func(v *SkillSnapshot) { reason := "removed"; v.Items[0].Resolution.ExclusionReason = &reason },
		"rule source":     func(v *SkillSnapshot) { v.Items[0].Resolution.RevisionSource = "current" },
		"duplicate":       func(v *SkillSnapshot) { v.Items = append(v.Items, v.Items[0]) },
		"checkpoint":      func(v *SkillSnapshot) { value := "bad"; v.StartingCheckpointID = &value },
	} {
		t.Run(name, func(t *testing.T) {
			view, _ := snapshotTestView(t, []byte("# instructions"))
			expected := view.SkillSnapshotIdentity
			change(&view)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(snapshotTestEnvelope(view))
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "node-token").GetSkillSnapshot(context.Background(), expected); err == nil {
				t.Fatal("invalid original snapshot was accepted")
			}
		})
	}
}

func TestSkillSnapshotRejectsAbsentNullAndNonIntegerFixedInputs(t *testing.T) {
	view, _ := snapshotTestView(t, []byte("# instructions"))
	for _, replacement := range []string{`null`, `true`, `"0"`, `0.0`, `9223372036854775808`} {
		t.Run(replacement, func(t *testing.T) {
			encoded, err := json.Marshal(snapshotTestEnvelope(view))
			if err != nil {
				t.Fatal(err)
			}
			encoded = bytes.Replace(encoded, []byte(`"library_generation":9007199254740993`), []byte(`"library_generation":`+replacement), 1)
			assertRejectedSnapshotJSON(t, view.SkillSnapshotIdentity, encoded)
		})
	}
	for _, field := range []string{"library_generation", "starting_checkpoint_id", "items", "manifest", "system_releases"} {
		t.Run("absent "+field, func(t *testing.T) {
			data, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, field)
			encoded, err := json.Marshal(map[string]any{"schema_version": 1, "status": "ready", "committed": false, "data": fields, "errors": []any{}})
			if err != nil {
				t.Fatal(err)
			}
			assertRejectedSnapshotJSON(t, view.SkillSnapshotIdentity, encoded)
		})
	}
}

func assertRejectedSnapshotJSON(t *testing.T, expected SkillSnapshotIdentity, encoded []byte) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(encoded) }))
	defer server.Close()
	if _, err := NewClient(server.URL, "node-token").GetSkillSnapshot(context.Background(), expected); err == nil {
		t.Fatal("malformed snapshot response was accepted")
	}
}

func TestSkillSnapshotManifestCanExceedSmallReceiptLimit(t *testing.T) {
	view, original := snapshotTestView(t, []byte("same content"))
	view.Items = []SkillSnapshotItem{}
	view.Manifest.Entries = make([]skillmanager.Entry, 15_000)
	for index := range view.Manifest.Entries {
		entry := original
		entry.Path = fmt.Sprintf("root-%06d-", index) + strings.Repeat("a", 180)
		view.Manifest.Entries[index] = entry
	}
	var err error
	view.TreeDigest, err = skillmanager.Digest(view.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshotTestEnvelope(view))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= maxSkillResponseBytes {
		t.Fatal("fixture did not cross ordinary receipt limit")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(encoded) }))
	defer server.Close()
	actual, err := NewClient(server.URL, "node-token").GetSkillSnapshot(context.Background(), view.SkillSnapshotIdentity)
	if err != nil || len(actual.Manifest.Entries) != len(view.Manifest.Entries) {
		t.Fatalf("large snapshot rejected: %v", err)
	}
}

func TestSkillSnapshotRejectsNonReadyOrCommittedEnvelope(t *testing.T) {
	view, _ := snapshotTestView(t, []byte("instructions"))
	for _, field := range []string{"version", "status", "committed", "missing commitment", "errors"} {
		t.Run(field, func(t *testing.T) {
			envelope := snapshotTestEnvelope(view)
			switch field {
			case "version":
				envelope.SchemaVersion = 2
			case "status":
				envelope.Status = "preparing"
			case "committed":
				*envelope.Committed = true
			case "missing commitment":
				envelope.Committed = nil
			case "errors":
				envelope.Errors = append(envelope.Errors, struct {
					Code string `json:"code"`
				}{Code: "SNAPSHOT_NOT_FOUND"})
			}
			encoded, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			assertRejectedSnapshotJSON(t, view.SkillSnapshotIdentity, encoded)
		})
	}
}

func TestSkillSnapshotRejectsCaseAliasedIdentitiesAndRules(t *testing.T) {
	view, _ := snapshotTestView(t, []byte("instructions"))
	for _, pair := range [][2]string{
		{`"library_generation":9007199254740993`, `"library_generation":9007199254740993,"Library_Generation":1`},
		{`"state_epoch":9007199254740997`, `"state_epoch":9007199254740997,"State_Epoch":1`},
		{`"included":true`, `"included":false,"Included":true`},
		{`"enabled":true`, `"Enabled":true`},
		{`"exclusion_reason":null`, `"exclusion_reason":null,"other":false`},
	} {
		encoded, err := json.Marshal(snapshotTestEnvelope(view))
		if err != nil {
			t.Fatal(err)
		}
		mutated := bytes.Replace(encoded, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(mutated, encoded) {
			t.Fatal("alias fixture did not change the response")
		}
		assertRejectedSnapshotJSON(t, view.SkillSnapshotIdentity, mutated)
	}
}
