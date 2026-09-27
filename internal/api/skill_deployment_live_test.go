package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// This opt-in test uses only an explicitly supplied disposable Server and never local production credentials.
func TestSkillDeploymentLiveServer(t *testing.T) {
	path := os.Getenv("AGENT_REMOTE_TEST_SKILL_DEPLOYMENT_FIXTURE")
	if path == "" {
		t.Skip("requires an isolated authenticated deployment fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read disposable fixture")
	}
	var fixture struct {
		URL           string                       `json:"url"`
		Token         string                       `json:"token"`
		Input         skillmanager.SkillDeployment `json:"input"`
		LeaseAttempt  int64                        `json:"lease_attempt"`
		ExpectedError string                       `json:"expected_error"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal("invalid disposable fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := NewClient(fixture.URL, fixture.Token)
	received, readErr := client.GetSkillDeployment(ctx, fixture.Input.SkillDeploymentIdentity, fixture.LeaseAttempt)
	lease, leaseErr := client.RenewSkillDeploymentLease(ctx, fixture.Input.SkillDeploymentIdentity, fixture.LeaseAttempt)
	failures := []error{readErr, leaseErr}
	count := 0
	for _, entry := range fixture.Input.Manifest.Entries {
		if entry.Kind != "file" {
			continue
		}
		failures = append(failures, client.ReadSkillDeploymentFile(ctx, fixture.Input.SkillDeploymentIdentity, fixture.LeaseAttempt, entry, io.Discard))
		count++
	}
	for _, err := range failures {
		if fixture.ExpectedError != "" {
			var failure *HTTPError
			if !errors.As(err, &failure) || failure.Code != fixture.ExpectedError {
				t.Fatal("revoked transport did not fail with expected authority code")
			}
		} else if err != nil {
			t.Fatal("deployment transport failed", err)
		}
	}
	if fixture.ExpectedError != "" {
		return
	}
	if count == 0 || received.Plan.Generation != fixture.Input.Plan.Generation || lease.SkillDeploymentIdentity != fixture.Input.SkillDeploymentIdentity {
		t.Fatal("live transfer changed original input or skipped file verification")
	}
	t.Logf("verified original input, %d complete files and exact current-attempt renewal", count)
}
