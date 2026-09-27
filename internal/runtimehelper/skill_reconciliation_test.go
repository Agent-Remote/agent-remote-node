package runtimehelper

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"testing"
	"time"
)

func TestHelperFinalizationReconcileStrictRequest(t *testing.T) {
	for _, data := range []string{
		`{"node_id":"n","session_id":"s"}`,
		`{"node_id":"n"}`,
		`{"node_id":"n","Session_ID":"s"}`,
		`{"node_id":"n","session_id":null}`,
		`{"node_id":"n","session_id":"s","command":"stop"}`,
		`{"node_id":"n","node_id":"n","session_id":"s"}`,
	} {
		var input skillReconciliationRequest
		err := json.Unmarshal([]byte(data), &input)
		if (err == nil) != (data == `{"node_id":"n","session_id":"s"}`) {
			t.Fatal("noncanonical reconciliation frame accepted", data, err)
		}
	}
}

func TestHelperFinalizationReconcileCancelsLockWait(t *testing.T) {
	for _, operation := range []string{skillReconciliationOperation, skillAdmissionDrainOperation} {
		for _, ending := range []string{"cancel", "deadline"} {
			t.Run(operation+"/"+ending, func(t *testing.T) {
				testSkillObservationCancelsLockWait(t, operation, ending == "cancel")
			})
		}
	}
}

func testSkillObservationCancelsLockWait(t *testing.T, operation string, cancelEarly bool) {
	t.Helper()
	file, err := os.CreateTemp("", "skill-reconcile-")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(path)
	server := NewServer(path, -1, os.Getuid(), Engine{})
	server.mu.Lock()
	defer server.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		connection, err := listener.Accept()
		if err == nil {
			server.handle(context.Background(), connection)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if cancelEarly {
		timer := time.AfterFunc(50*time.Millisecond, cancel)
		defer timer.Stop()
	}
	defer cancel()
	if _, err := NewClient(path).observeSkillSession(ctx, "blocked", operation, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("cancelled reconciliation succeeded")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled reconciliation retained its blocked handler")
	}
}

func TestHelperFinalizationAdmissionRejectsChangedObservation(t *testing.T) {
	const nodeID = "11111111-1111-4111-8111-111111111111"
	const sessionID = "22222222-2222-4222-8222-222222222222"
	for _, result := range []string{
		`{"session_id":"` + sessionID + `","state":"running","record":null}`,
		`{"session_id":"` + nodeID + `","state":"running","record":null}`,
		`{"session_id":"` + sessionID + `","state":"finalized","record":null}`,
		`{"session_id":"` + sessionID + `","state":"running"}`,
		`{"session_id":"` + sessionID + `","state":null,"record":null}`,
		`{"session_id":"` + sessionID + `","State":"running","record":null}`,
		`{"session_id":"` + sessionID + `","state":"running","state":"running","record":null}`,
		`{"session_id":"` + sessionID + `","state":"running","record":null,"admitted":true}`,
	} {
		t.Run(result, func(t *testing.T) {
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				defer connection.Close()
				var request Request
				if err := json.NewDecoder(connection).Decode(&request); err != nil || request.Operation != skillAdmissionDrainOperation {
					t.Error("unexpected drain request", err)
					return
				}
				_, _ = fmt.Fprintf(connection, `{"version":1,"ok":true,"result":%s}`+"\n", result)
			})
			observation, err := client.DrainUnadmittedSkillSession(context.Background(), "drain", nodeID, sessionID)
			valid := result == `{"session_id":"`+sessionID+`","state":"running","record":null}`
			if (err == nil) != valid || valid && observation.State != "running" {
				t.Fatal("changed drain response accepted", observation, err)
			}
		})
	}
}

func TestHelperCapturePendingStrictResponse(t *testing.T) {
	binding := skillmanager.SnapshotBinding{NodeID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", AccountID: "33333333-3333-4333-8333-333333333333", SessionID: "44444444-4444-4444-8444-444444444444", SnapshotID: "55555555-5555-4555-8555-555555555555", TaskID: "66666666-6666-4666-8666-666666666666", PreparationDigest: strings.Repeat("c", 64), InitialTreeDigest: strings.Repeat("a", 64), DirectoryEpoch: 1, LibraryGeneration: 1}
	encoded, err := json.Marshal(SkillSessionObservation{SessionID: binding.SessionID, State: "capture_pending", Pending: &skillmanager.CaptureFailure{Binding: binding, Code: "quota_exceeded"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"", "alias", "null", "duplicate", "unknown", "raw", "running", "missing_classification"} {
		t.Run(fault, func(t *testing.T) {
			body := string(encoded)
			switch fault {
			case "alias":
				body = strings.Replace(body, `"capture_pending":`, `"Capture_Pending":`, 1)
			case "null":
				body = strings.Replace(body, `"unclean":false`, `"unclean":null`, 1)
			case "duplicate":
				body = strings.Replace(body, `"unclean":false`, `"unclean":false,"unclean":false`, 1)
			case "unknown":
				body = strings.Replace(body, `"quota_exceeded"`, `"secret error"`, 1)
			case "raw":
				body = strings.Replace(body, `"code":`, `"message":"secret error","code":`, 1)
			case "running":
				body = strings.Replace(body, `"state":"capture_pending"`, `"state":"running"`, 1)
			case "missing_classification":
				body = strings.Replace(body, `,"unclean":false`, ``, 1)
			}
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				defer connection.Close()
				var request Request
				if err := json.NewDecoder(connection).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				_, _ = fmt.Fprintf(connection, `{"version":1,"ok":true,"result":%s}`+"\n", body)
			})
			observation, err := client.ReconcileSkillSession(context.Background(), "pending", binding.NodeID, binding.SessionID)
			if (err != nil) != (fault != "") || err == nil && observation.Pending == nil {
				t.Fatal("unsafe capture observation", fault, err)
			}
		})
	}
}
