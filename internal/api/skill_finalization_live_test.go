package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestSkillFinalizationLiveServer(t *testing.T) {
	path := os.Getenv("AGENT_REMOTE_FINALIZATION_FIXTURE")
	if path == "" {
		t.Skip("requires a disposable authenticated Server fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("fixture unavailable")
	}
	var fixture struct {
		URL      string                           `json:"url"`
		Token    string                           `json:"token"`
		Input    SkillFinalizationInput           `json:"input"`
		Manifest skillmanager.Manifest            `json:"manifest"`
		Content  []byte                           `json:"content"`
		Previous *SkillFinalization               `json:"previous"`
		Capture  *skillmanager.FinalizationRecord `json:"capture"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal("fixture invalid")
	}
	client, ctx := NewClient(fixture.URL, fixture.Token), context.Background()
	if fixture.Capture != nil {
		for range 2 {
			if err := client.ObserveSkillTermination(ctx, *fixture.Capture); err != nil {
				t.Fatal("exact termination confirmation failed", err)
			}
		}
	}
	if fixture.Previous != nil {
		observed, err := client.GetSkillFinalization(ctx, fixture.Input, *fixture.Previous)
		if err != nil || observed.UploadID != fixture.Previous.UploadID || observed.UploadStatus != "expired" {
			t.Fatal("status renewed an expired upload", err)
		}
	}
	plan, err := client.BeginSkillFinalization(ctx, fixture.Input, fixture.Manifest, fixture.Previous)
	if err != nil || plan.Status != "upload_pending" {
		t.Fatal("begin failed", err)
	}
	if fixture.Previous != nil {
		previous := *fixture.Previous
		if plan.ID != previous.ID || plan.UploadID == previous.UploadID || plan.UploadAttempt != previous.UploadAttempt+1 {
			t.Fatal("renewal changed finalization or failed to advance the upload attempt")
		}
		if _, err := client.CompleteSkillFinalization(ctx, fixture.Input, previous); err == nil {
			t.Fatal("superseded upload completed")
		}
		for _, entry := range fixture.Manifest.Entries {
			if entry.Kind == "file" {
				if err := client.PutSkillFinalizationFile(ctx, fixture.Input, previous, entry, io.NopCloser(bytes.NewReader(fixture.Content))); err == nil {
					t.Fatal("superseded upload accepted content")
				}
			}
		}
	}
	if _, err := client.CompleteSkillFinalization(ctx, fixture.Input, plan); err == nil {
		t.Fatal("incomplete input accepted")
	}
	for _, entry := range fixture.Manifest.Entries {
		if entry.Kind == "file" {
			if err := client.PutSkillFinalizationFile(ctx, fixture.Input, plan, entry, io.NopCloser(bytes.NewReader(fixture.Content))); err != nil {
				t.Fatal("retained upload failed", err)
			}
		}
	}
	retained, err := client.CompleteSkillFinalization(ctx, fixture.Input, plan)
	if err != nil || retained.CheckpointID == nil {
		t.Fatal("complete failed", err)
	}
	replayed, err := client.BeginSkillFinalization(ctx, fixture.Input, fixture.Manifest, &retained)
	if err != nil || replayed.CheckpointID == nil || *replayed.CheckpointID != *retained.CheckpointID {
		t.Fatal("begin replay changed retained input", err)
	}
	publication, err := client.PublishSkillFinalization(ctx, fixture.Input, retained)
	want := "published"
	if fixture.Input.Unclean {
		want = "detached"
	}
	if err != nil || publication.Status != want {
		t.Fatal("publication failed", err)
	}
	again, err := client.PublishSkillFinalization(ctx, fixture.Input, retained)
	if err != nil || again.ID != publication.ID || again.Status != publication.Status || again.Attempt != publication.Attempt {
		t.Fatal("publication replay changed decision", err)
	}
	observed, err := client.GetSkillFinalization(ctx, fixture.Input, retained)
	if err != nil || observed.Status != want || observed.CheckpointID == nil || *observed.CheckpointID != *retained.CheckpointID {
		t.Fatal("status lost publication or incoming checkpoint", err)
	}
	changed := fixture.Input
	changed.Unclean = !changed.Unclean
	if _, err := client.BeginSkillFinalization(ctx, changed, fixture.Manifest, nil); err == nil {
		t.Fatal("Server accepted changed termination classification")
	}
}
