package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func deploymentTestView(t *testing.T) skillmanager.SkillDeployment {
	t.Helper()
	data, err := os.ReadFile("../skillmanager/testdata/deployment-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var view skillmanager.SkillDeployment
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func deploymentTestEnvelope[T any](status string, view T) skillEnvelope[T] {
	committed := false
	return skillEnvelope[T]{SchemaVersion: 1, Status: status, Committed: &committed, Data: view}
}

func deploymentTestLease(view skillmanager.SkillDeployment) SkillDeploymentLease {
	now := time.Now().UTC()
	return SkillDeploymentLease{SkillDeploymentIdentity: view.SkillDeploymentIdentity, LeaseAttempt: 3,
		ServerTime: now, LeaseUntil: now.Add(time.Minute), RenewAfterMilliseconds: 20000}
}

func TestDeploymentTransportUsesOriginalIdentityAndCurrentPollAttempt(t *testing.T) {
	view := deploymentTestView(t)
	content := []byte("---\nname: learning\ndescription: example\n---\n")
	entry := view.Manifest.Entries[1]
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer node-test-token" || r.URL.Query().Get("task_id") != view.TaskID {
			t.Error("lost original task authentication")
		}
		if r.Method == http.MethodPost {
			var body struct {
				LeaseAttempt int64 `json:"lease_attempt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LeaseAttempt != 3 || r.URL.Query().Has("lease_attempt") {
				t.Error("lease did not send one exact poll attempt")
			}
			_ = json.NewEncoder(w).Encode(deploymentTestEnvelope("leased", deploymentTestLease(view)))
			return
		}
		if r.URL.Query().Get("lease_attempt") != "3" {
			t.Error("download omitted poll attempt")
		}
		if strings.Contains(r.URL.Path, "/files/") {
			if r.URL.Path != "/api/v1/node/skill-deployments/"+view.AttemptID+"/files/"+entry.SHA256 {
				t.Error("changed file or deployment identity")
			}
			snapshotFileHeaders(w, entry)
			_, _ = w.Write(content)
		} else {
			if r.URL.Path != "/api/v1/node/skill-deployments/"+view.AttemptID {
				t.Error("changed deployment identity")
			}
			_ = json.NewEncoder(w).Encode(deploymentTestEnvelope("prepared_input", view))
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "node-test-token")
	actual, err := client.GetSkillDeployment(context.Background(), view.SkillDeploymentIdentity, 3)
	if err != nil || actual.Plan.Generation != view.Plan.Generation || actual.Items[0].StateEpoch != view.Items[0].StateEpoch {
		t.Fatal("input validation or integer precision failed", err)
	}
	var target bytes.Buffer
	if err := client.ReadSkillDeploymentFile(context.Background(), view.SkillDeploymentIdentity, 3, entry, &target); err != nil || !bytes.Equal(target.Bytes(), content) {
		t.Fatal("original file transfer failed", err)
	}
	lease, err := client.RenewSkillDeploymentLease(context.Background(), view.SkillDeploymentIdentity, 3)
	if err != nil || lease.SkillDeploymentIdentity != view.SkillDeploymentIdentity {
		t.Fatal("lease binding failed", err)
	}
	if calls.Load() != 3 {
		t.Fatal("transport unexpectedly replayed a request")
	}
}

func TestDeploymentRejectsChangedInputAndMalformedAuthority(t *testing.T) {
	for name, change := range map[string]func(*skillmanager.SkillDeployment){
		"task":           func(d *skillmanager.SkillDeployment) { d.TaskID = d.OperationID },
		"attempt":        func(d *skillmanager.SkillDeployment) { d.AttemptID = d.OperationID },
		"owner":          func(d *skillmanager.SkillDeployment) { d.UserID = d.OperationID },
		"checkpoint":     func(d *skillmanager.SkillDeployment) { d.CheckpointID = d.OperationID },
		"plan":           func(d *skillmanager.SkillDeployment) { d.Plan.Generation++ },
		"tree":           func(d *skillmanager.SkillDeployment) { d.Manifest.Entries[0].Mode = 0700 },
		"member":         func(d *skillmanager.SkillDeployment) { d.Items = nil },
		"omitted member": func(d *skillmanager.SkillDeployment) { d.Items = []skillmanager.SkillDeploymentMember{} },
		"duplicate":      func(d *skillmanager.SkillDeployment) { d.Items = append(d.Items, d.Items[0]) },
		"state epoch":    func(d *skillmanager.SkillDeployment) { d.Items[0].StateEpoch = 0 },
		"disabled":       func(d *skillmanager.SkillDeployment) { d.Plan.Sources[0].Enabled = false },
	} {
		t.Run(name, func(t *testing.T) {
			view := deploymentTestView(t)
			original := view.SkillDeploymentIdentity
			change(&view)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(deploymentTestEnvelope("prepared_input", view))
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "test").GetSkillDeployment(context.Background(), original, 3); err == nil {
				t.Fatal("changed deployment input was accepted")
			}
		})
	}
}

func TestDeploymentRejectsInvalidLeaseAcknowledgements(t *testing.T) {
	view := deploymentTestView(t)
	for name, change := range map[string]func(*SkillDeploymentLease){
		"task":      func(l *SkillDeploymentLease) { l.TaskID = l.OperationID },
		"tree":      func(l *SkillDeploymentLease) { l.TreeDigest = strings.Repeat("0", 64) },
		"attempt":   func(l *SkillDeploymentLease) { l.LeaseAttempt++ },
		"expired":   func(l *SkillDeploymentLease) { l.LeaseUntil = l.ServerTime },
		"unbounded": func(l *SkillDeploymentLease) { l.LeaseUntil = l.ServerTime.Add(301 * time.Second) },
		"interval":  func(l *SkillDeploymentLease) { l.RenewAfterMilliseconds = 60000 },
	} {
		t.Run(name, func(t *testing.T) {
			lease := deploymentTestLease(view)
			change(&lease)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(deploymentTestEnvelope("leased", lease))
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "test").RenewSkillDeploymentLease(context.Background(), view.SkillDeploymentIdentity, 3); err == nil {
				t.Fatal("invalid lease was accepted")
			}
		})
	}
}

func TestDeploymentTransportNeverFollowsRedirectsOrLeaksErrorBodies(t *testing.T) {
	view := deploymentTestView(t)
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	for _, redirect := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirect {
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
				return
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"errors":[{"code":"DEPLOYMENT_LEASE_CHANGED","message":"private bytes"}]}`)
		}))
		client := NewClient(server.URL, "test-private-token")
		_, read := client.GetSkillDeployment(context.Background(), view.SkillDeploymentIdentity, 3)
		file := client.ReadSkillDeploymentFile(context.Background(), view.SkillDeploymentIdentity, 3, view.Manifest.Entries[1], io.Discard)
		_, lease := client.RenewSkillDeploymentLease(context.Background(), view.SkillDeploymentIdentity, 3)
		server.Close()
		for _, err := range []error{read, file, lease} {
			var response *HTTPError
			if !errors.As(err, &response) || strings.Contains(err.Error(), "private") {
				t.Fatal("unsafe or absent response error")
			}
			if !redirect && response.Code != "DEPLOYMENT_LEASE_CHANGED" {
				t.Fatal("lost authorization failure")
			}
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect received deployment credentials")
	}
}

func TestDeploymentInvalidRequestsDoNotReachTransport(t *testing.T) {
	view := deploymentTestView(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer server.Close()
	client := NewClient(server.URL, "test")
	for _, attempt := range []int64{0, -1, 2147483648} {
		if _, err := client.GetSkillDeployment(context.Background(), view.SkillDeploymentIdentity, attempt); err == nil {
			t.Fatal("invalid poll attempt")
		}
		if _, err := client.RenewSkillDeploymentLease(context.Background(), view.SkillDeploymentIdentity, attempt); err == nil {
			t.Fatal("invalid lease attempt")
		}
		if err := client.ReadSkillDeploymentFile(context.Background(), view.SkillDeploymentIdentity, attempt, view.Manifest.Entries[1], io.Discard); err == nil {
			t.Fatal("invalid file poll attempt")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached server")
	}
}
