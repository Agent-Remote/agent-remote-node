package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// This opt-in contract consumes only an explicitly supplied disposable Server fixture.
func TestSkillSnapshotLiveServer(t *testing.T) {
	path := os.Getenv("AGENT_REMOTE_TEST_SKILL_SNAPSHOT_FIXTURE")
	if path == "" {
		t.Skip("requires an isolated authenticated Server snapshot fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		URL          string        `json:"url"`
		Token        string        `json:"token"`
		Snapshot     SkillSnapshot `json:"snapshot"`
		Denied       bool          `json:"denied"`
		LeaseAttempt int64         `json:"lease_attempt"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal("invalid isolated Server fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := NewClient(fixture.URL, fixture.Token)
	if fixture.LeaseAttempt > 0 {
		lease, err := client.RenewSkillSnapshotLease(ctx, fixture.Snapshot.SkillSnapshotIdentity, fixture.LeaseAttempt)
		if fixture.Denied {
			var failure *HTTPError
			if !errors.As(err, &failure) || failure.StatusCode != http.StatusNotFound || failure.Code != "SNAPSHOT_NOT_FOUND" {
				t.Fatal("expired preparation lease was revived")
			}
		} else if err != nil || lease.SkillSnapshotIdentity != fixture.Snapshot.SkillSnapshotIdentity || lease.LeaseAttempt != fixture.LeaseAttempt {
			t.Fatal("Server/Node lease contract failed", err)
		}
	}
	snapshot, err := client.GetSkillSnapshot(ctx, fixture.Snapshot.SkillSnapshotIdentity)
	if fixture.Denied {
		var failure *HTTPError
		if !errors.As(err, &failure) || failure.StatusCode != http.StatusNotFound || failure.Code != "SNAPSHOT_NOT_FOUND" {
			t.Fatal("expired preparation remained readable")
		}
		for _, entry := range fixture.Snapshot.Manifest.Entries {
			if entry.Kind != "file" {
				continue
			}
			err = client.ReadSkillSnapshotFile(ctx, fixture.Snapshot.SkillSnapshotIdentity, entry, io.Discard)
			if !errors.As(err, &failure) || failure.StatusCode != http.StatusNotFound || failure.Code != "SNAPSHOT_NOT_FOUND" {
				t.Fatal("expired preparation file remained readable")
			}
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TreeDigest != fixture.Snapshot.TreeDigest || snapshot.LibraryGeneration != fixture.Snapshot.LibraryGeneration || snapshot.DirectoryEpoch != fixture.Snapshot.DirectoryEpoch {
		t.Fatal("Server changed fixed snapshot inputs")
	}
	files := 0
	for _, entry := range snapshot.Manifest.Entries {
		if entry.Kind != "file" {
			continue
		}
		if err := client.ReadSkillSnapshotFile(ctx, snapshot.SkillSnapshotIdentity, entry, io.Discard); err != nil {
			t.Fatal(err)
		}
		files++
	}
	if files == 0 {
		t.Fatal("fixture did not exercise content transfer")
	}
}
