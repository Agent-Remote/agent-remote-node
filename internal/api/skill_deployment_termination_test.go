package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func deploymentTerminationFixture(t *testing.T) SkillDeploymentTerminatedResult {
	t.Helper()
	binding := deploymentTestView(t).SkillDeploymentIdentity
	return SkillDeploymentTerminatedResult{Intent: SkillDeploymentTerminationIntent{
		Version: 1, IntentID: binding.OperationID, Binding: binding,
		Request: SkillDeploymentTerminationRequest{LeaseAttempt: 3, ErrorCode: "TRANSFER_FAILED"},
		Outcome: "failed", ErrorCode: "TRANSFER_FAILED", Retryable: true,
	}, Drain: skillmanager.DeploymentDrain{Version: 1, Binding: binding, HelperReceiptID: binding.TaskID}}
}

func TestDeploymentTerminationTransportPreservesOriginalIntentAndDrain(t *testing.T) {
	result := deploymentTerminationFixture(t)
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer original-node" || r.URL.Query().Get("task_id") != result.Intent.Binding.TaskID || r.URL.Query().Has("lease_attempt") {
			t.Error("lost original authority")
		}
		status, committed := "observed", false
		var data any
		switch {
		case r.Method == http.MethodGet:
			data = deploymentTerminationLookup{Intent: &result.Intent}
		case strings.HasSuffix(r.URL.Path, "/termination"):
			var request SkillDeploymentTerminationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request != result.Intent.Request {
				t.Error("changed termination request", err)
			}
			data = result.Intent
			status = "drain_required"
			committed = true
			writes++
		default:
			var request SkillDeploymentTerminatedResult
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request != result {
				t.Error("changed terminal proposal", err)
			}
			data = SkillDeploymentTerminationObservation{Result: result, Accepted: true, CurrentLeaseAttempt: 4, TaskStatus: "failed"}
			if !strings.HasSuffix(r.URL.Path, "/inspect") {
				status = "confirmed"
				committed = true
				writes++
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": status, "committed": committed, "data": data, "errors": []any{}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "original-node")
	intent, err := client.RequestSkillDeploymentTermination(context.Background(), result.Intent.Binding, result.Intent.Request)
	if err != nil || intent != result.Intent {
		t.Fatal("revocation failed", err)
	}
	read, err := client.GetSkillDeploymentTermination(context.Background(), result.Intent.Binding)
	if err != nil || read == nil || *read != intent {
		t.Fatal("lookup failed", err)
	}
	accepted, err := client.ConfirmSkillDeploymentTermination(context.Background(), result)
	if err != nil || !accepted.Accepted || accepted.CurrentLeaseAttempt != 4 {
		t.Fatal("confirmation failed", err)
	}
	observed, err := client.InspectSkillDeploymentTermination(context.Background(), result)
	if err != nil || observed != accepted || writes != 2 {
		t.Fatal("inspection changed or resubmitted result", err, writes)
	}
}

func TestDeploymentTerminationRejectsAmbiguousAndChangedReceipts(t *testing.T) {
	result := deploymentTerminationFixture(t)
	for _, kind := range []string{"missing", "alias", "null", "duplicate", "fraction", "identity", "retryable", "status", "poll", "unaccepted"} {
		t.Run(kind, func(t *testing.T) {
			observation := SkillDeploymentTerminationObservation{Result: result, Accepted: true, CurrentLeaseAttempt: 3, TaskStatus: "failed"}
			raw, _ := json.Marshal(observation)
			data := string(raw)
			switch kind {
			case "missing":
				data = strings.Replace(data, `"accepted":true,`, "", 1)
			case "alias":
				data = strings.Replace(data, `"intent_id"`, `"Intent_ID"`, 1)
			case "null":
				data = strings.Replace(data, `"retryable":true`, `"retryable":null`, 1)
			case "duplicate":
				data = strings.Replace(data, `"version":1`, `"version":1,"version":1`, 1)
			case "fraction":
				data = strings.Replace(data, `"lease_attempt":3`, `"lease_attempt":3.0`, 1)
			case "identity":
				data = strings.Replace(data, result.Drain.Binding.TreeDigest, strings.Repeat("f", 64), 1)
			case "retryable":
				data = strings.Replace(data, `"retryable":true`, `"retryable":false`, 1)
			case "status":
				data = strings.Replace(data, `"task_status":"failed"`, `"task_status":"cancelled"`, 1)
			case "poll":
				data = strings.Replace(data, `"current_lease_attempt":3`, `"current_lease_attempt":2`, 1)
			case "unaccepted":
				data = strings.Replace(data, `"accepted":true`, `"accepted":false`, 1)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = w.Write([]byte(`{"schema_version":1,"status":"confirmed","committed":true,"errors":[],"data":` + data + `}`))
			}))
			defer server.Close()
			if _, err := NewClient(server.URL, "original").ConfirmSkillDeploymentTermination(context.Background(), result); err == nil || calls != 1 {
				t.Fatal("invalid confirmation accepted or retried", err, calls)
			}
		})
	}
}

func TestDeploymentTerminationLookupDistinguishesAbsentFromMalformed(t *testing.T) {
	result := deploymentTerminationFixture(t)
	for _, data := range []string{`{"intent":null}`, `{}`, `{"Intent":null}`, `{"intent":null,"intent":null}`, `{"intent":null,"extra":true}`} {
		t.Run(data, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"schema_version":1,"status":"observed","committed":false,"errors":[],"data":` + data + `}`))
			}))
			defer server.Close()
			intent, err := NewClient(server.URL, "original").GetSkillDeploymentTermination(context.Background(), result.Intent.Binding)
			if data == `{"intent":null}` {
				if err != nil || intent != nil {
					t.Fatal("valid absence rejected", err)
				}
			} else if err == nil {
				t.Fatal("malformed absence accepted")
			}
		})
	}
}

func TestDeploymentTerminationSupersededIsNeverRetryable(t *testing.T) {
	result := deploymentTerminationFixture(t)
	result.Intent.Outcome = "superseded"
	result.Intent.ErrorCode = "OPERATION_SUPERSEDED"
	result.Intent.Retryable = false
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	result.Intent.Retryable = true
	if err := result.Validate(); err == nil {
		t.Fatal("retryable supersession accepted")
	}
	result = deploymentTerminationFixture(t)
	result.Intent.Request.ErrorCode = "OPERATION_SUPERSEDED"
	result.Intent.ErrorCode = "OPERATION_SUPERSEDED"
	result.Intent.Retryable = false
	if err := result.Validate(); err == nil {
		t.Fatal("fake failed supersession accepted")
	}
}

func TestDeploymentTerminationPythonGoldenResponses(t *testing.T) {
	data, err := os.ReadFile("testdata/skill-deployment-termination-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var observations []SkillDeploymentTerminationObservation
	if err := json.Unmarshal(data, &observations); err != nil || len(observations) != 2 {
		t.Fatal("Python response vectors invalid", err)
	}
	for _, observed := range observations {
		t.Run(observed.Result.Intent.Outcome, func(t *testing.T) {
			if observed.Result.Validate() != nil || !observed.Accepted || observed.CurrentLeaseAttempt != 4 || observed.Result.Intent.Request.LeaseAttempt != 3 {
				t.Fatal("Python original authority changed")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "confirmed", "committed": true, "errors": []any{}, "data": observed})
			}))
			defer server.Close()
			actual, err := NewClient(server.URL, "fixture").ConfirmSkillDeploymentTermination(context.Background(), observed.Result)
			if err != nil || actual != observed {
				t.Fatal("Python Server response did not preserve original result", err)
			}
		})
	}
}

func TestDeploymentTerminationMissingDataCannotBecomeAbsence(t *testing.T) {
	result := deploymentTerminationFixture(t)
	for _, body := range []string{
		`{"schema_version":1,"status":"observed","committed":false,"errors":[]}`,
		`{"schema_version":1,"status":"observed","committed":false,"errors":[],"Data":{"intent":null}}`,
		`{"schema_version":1,"status":"observed","committed":false,"errors":[],"data":{"intent":null},"DATA":{"intent":null}}`,
		`{"schema_version":1,"status":"observed","committed":false,"errors":null,"data":{"intent":null}}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			if _, err := NewClient(server.URL, "original").GetSkillDeploymentTermination(context.Background(), result.Intent.Binding); err == nil {
				t.Fatal("malformed envelope became an unrevoked observation")
			}
		})
	}
}
