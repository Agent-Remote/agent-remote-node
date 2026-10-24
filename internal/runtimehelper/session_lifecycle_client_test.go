package runtimehelper

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestSessionLifecycleClientRequiresExactPrivateObservation(t *testing.T) {
	valid := `{"protocol_version":1,"session_id":"session-fixture","runtime_backend":"native","receipt_replay":false,"fresh_admission":true,"stop_intent":false}`
	cases := map[string]string{"valid": valid, "missing": strings.Replace(valid, `,"stop_intent":false`, "", 1), "null": strings.Replace(valid, `"stop_intent":false`, `"stop_intent":null`, 1), "alias": strings.Replace(valid, `"receipt_replay"`, `"Receipt_Replay"`, 1), "extra": strings.Replace(valid, `{"protocol`, `{"extra":false,"protocol`, 1), "duplicate": strings.Replace(valid, `{"protocol`, `{"session_id":"session-fixture","protocol`, 1), "fractional_version": strings.Replace(valid, `version":1`, `version":1.0`, 1), "replay_grant": strings.Replace(valid, `"receipt_replay":false`, `"receipt_replay":true`, 1), "closed_grant": strings.Replace(valid, `"stop_intent":false`, `"stop_intent":true`, 1), "wrong_session": strings.Replace(valid, "session-fixture", "other-session", 1), "wrong_backend": strings.Replace(valid, "native", "docker_sandbox", 1)}
	for name, observation := range cases {
		t.Run(name, func(t *testing.T) {
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				_, _ = bufio.NewReader(connection).ReadBytes('\n')
				_, _ = fmt.Fprintf(connection, "{\"version\":1,\"ok\":true,\"result\":{\"status\":\"running\"},\"session_execution\":%s}\n", observation)
			})
			_, err := client.CallSession(context.Background(), "original-task", "start_session", map[string]any{"session_id": "session-fixture", "runtime_backend": "native"})
			if (err == nil) != (name == "valid") {
				t.Fatalf("unexpected metadata acceptance: %v", err)
			}
		})
	}
}

func TestSessionLifecycleClientRejectsLaunchGrantForStop(t *testing.T) {
	client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		_, _ = bufio.NewReader(connection).ReadBytes('\n')
		_, _ = fmt.Fprintln(connection, `{"version":1,"ok":true,"result":{"status":"stopped"},"session_execution":{"protocol_version":1,"session_id":"session-fixture","runtime_backend":"native","receipt_replay":false,"fresh_admission":true,"stop_intent":false}}`)
	})
	if _, err := client.CallSession(context.Background(), "stop-task", "stop_session", map[string]any{"session_id": "session-fixture", "runtime_backend": "native"}); err == nil {
		t.Fatal("stop response granted launch admission")
	}
}
