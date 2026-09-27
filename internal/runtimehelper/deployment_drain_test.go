package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestDeploymentDrainRequestRejectsAmbiguousOrForeignBindings(t *testing.T) {
	input := preparationTestDeployment(t, []byte("original"))
	for _, kind := range []string{"valid", "alias", "missing", "null", "extra", "version", "operation", "request", "node", "backend"} {
		t.Run(kind, func(t *testing.T) {
			payload, _ := Map(input.SkillDeploymentIdentity)
			request := Request{Version: 1, Operation: deploymentDrainOperation, RequestID: "drain_original", Payload: payload}
			switch kind {
			case "alias":
				payload["NodeID"] = payload["node_id"]
				delete(payload, "node_id")
			case "missing":
				delete(payload, "tree_digest")
			case "null":
				payload["account_id"] = nil
			case "extra":
				payload["path"] = "/tmp/arbitrary"
			case "version":
				request.Version = 2
			case "operation":
				request.Operation = deploymentPreparationOperation
			case "request":
				request.RequestID = "../arbitrary"
			case "node":
				payload["node_id"] = input.TaskID
			case "backend":
				payload["runtime_backend"] = "docker_sandbox"
			}
			result, err := validateDeploymentDrainRequest(context.Background(), request, input.NodeID)
			if kind == "valid" {
				if err != nil || result != input.SkillDeploymentIdentity {
					t.Fatal("original rejected", err)
				}
			} else if err == nil {
				t.Fatal("invalid drain request accepted")
			}
		})
	}
}

func TestDeploymentDrainClientValidatesExactReceipt(t *testing.T) {
	input := preparationTestDeployment(t, []byte("original"))
	receipt := skillmanager.DeploymentDrain{Version: 1, Binding: input.SkillDeploymentIdentity, HelperReceiptID: input.TaskID}
	encoded, _ := json.Marshal(receipt)
	for _, kind := range []string{"valid", "identity", "alias", "null", "duplicate", "fractional", "unknown", "missing", "result_extra"} {
		t.Run(kind, func(t *testing.T) {
			data := string(encoded)
			switch kind {
			case "identity":
				data = strings.Replace(data, input.TreeDigest, strings.Repeat("f", 64), 1)
			case "alias":
				data = strings.Replace(data, `"binding"`, `"Binding"`, 1)
			case "null":
				data = "null"
			case "duplicate":
				data = strings.Replace(data, `"version":1`, `"version":1,"version":1`, 1)
			case "fractional":
				data = strings.Replace(data, `"version":1`, `"version":1.0`, 1)
			case "unknown":
				data = strings.TrimSuffix(data, "}") + `,"path":"/tmp/x"}`
			case "missing":
				data = `{}`
			}
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				line, err := readBoundedLine(bufio.NewReader(connection), 4096)
				if err != nil {
					t.Error(err)
					return
				}
				var request Request
				if err := json.Unmarshal(line, &request); err != nil {
					t.Error(err)
					return
				}
				if _, err := validateDeploymentDrainRequest(context.Background(), request, input.NodeID); err != nil {
					t.Error(err)
					return
				}
				extra := ""
				if kind == "result_extra" {
					extra = `,"ignored":true`
				}
				_, _ = connection.Write([]byte(`{"version":1,"ok":true,"result":{"receipt":` + data + extra + "}}\n"))
			})
			actual, err := client.DrainSkillDeployment(context.Background(), "drain_original", input.SkillDeploymentIdentity)
			if kind == "valid" {
				if err != nil || actual != receipt {
					t.Fatal("valid receipt rejected", err)
				}
			} else if err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}
