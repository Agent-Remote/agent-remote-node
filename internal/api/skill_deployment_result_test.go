package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func deploymentResultFixture(t *testing.T) SkillDeploymentPreparedResult {
	t.Helper()
	input := deploymentTestView(t)
	digest, err := input.InputDigest()
	if err != nil {
		t.Fatal(err)
	}
	return SkillDeploymentPreparedResult{LeaseAttempt: 3, Preparation: skillmanager.DeploymentPreparation{Version: 1,
		Binding: input.SkillDeploymentIdentity, InputDigest: digest, DirectoryEpoch: input.DirectoryEpoch, Generation: input.Plan.Generation, HelperReceiptID: input.NodeID}}
}

func TestDeploymentResultConfirmAndReadOnlyInspectionKeepExactProposal(t *testing.T) {
	result := deploymentResultFixture(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer original-node" || r.URL.Query().Get("task_id") != result.Preparation.Binding.TaskID || r.URL.Query().Has("lease_attempt") {
			t.Error("lost result authority")
		}
		var received SkillDeploymentPreparedResult
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil || received != result {
			t.Error("changed result proposal", err)
		}
		inspect := strings.HasSuffix(r.URL.Path, "/inspect")
		status := "confirmed"
		if inspect {
			status = "observed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": status, "committed": !inspect, "errors": []any{}, "data": SkillDeploymentResultObservation{Result: result, Accepted: true, CurrentLeaseAttempt: 3, TaskStatus: "succeeded"}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "original-node")
	confirmed, err := client.ConfirmSkillDeployment(context.Background(), result)
	if err != nil || !confirmed.Accepted {
		t.Fatal("confirmation failed", err)
	}
	observed, err := client.InspectSkillDeployment(context.Background(), result)
	if err != nil || observed != confirmed || calls != 2 {
		t.Fatal("inspection changed or retried proposal", err, calls)
	}
}

func TestDeploymentResultRejectsMalformedAndInconsistentObservations(t *testing.T) {
	result := deploymentResultFixture(t)
	for _, kind := range []string{"foreign", "missing", "null", "alias", "duplicate", "backward", "future_accepted", "false_success", "unknown_status", "fraction"} {
		t.Run(kind, func(t *testing.T) {
			observation := SkillDeploymentResultObservation{Result: result, Accepted: true, CurrentLeaseAttempt: 3, TaskStatus: "succeeded"}
			switch kind {
			case "foreign":
				observation.Result.Preparation.HelperReceiptID = result.Preparation.Binding.TaskID
			case "backward":
				observation.CurrentLeaseAttempt = 2
			case "future_accepted":
				observation.CurrentLeaseAttempt = 4
			case "false_success":
				observation.Accepted = false
			case "unknown_status":
				observation.Accepted, observation.TaskStatus = false, "unknown"
			}
			data, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "observed", "committed": false, "errors": []any{}, "data": observation})
			text := string(data)
			switch kind {
			case "missing":
				text = strings.Replace(text, `"accepted":true,`, "", 1)
			case "null":
				text = strings.Replace(text, `"accepted":true`, `"accepted":null`, 1)
			case "alias":
				text = strings.Replace(text, `"accepted"`, `"Accepted"`, 1)
			case "duplicate":
				text = strings.Replace(text, `"accepted":true`, `"accepted":true,"accepted":true`, 1)
			case "fraction":
				text = strings.Replace(text, `"current_lease_attempt":3`, `"current_lease_attempt":3.0`, 1)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(text)) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "test").InspectSkillDeployment(context.Background(), result); err == nil {
				t.Fatal("invalid result observation accepted")
			}
		})
	}
}
