package runtimehelper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path"
	"strings"
	"testing"
	"time"
)

func TestManagedLaunchClientValidatesReceipt(t *testing.T) {
	input := managedSpecClientInput(t)
	input.Session.TmuxSessionName = "original-session"
	original := map[string]any{
		"status": "running", "session_id": input.Snapshot.SessionID,
		"tool_account_id": input.Snapshot.AccountID, "tool_type": "claude",
		"tmux_session_name": input.Session.TmuxSessionName, "tmux_started": true,
		"sandbox_name": "", "container_id": "", "runtime_backend": "native",
		"runtime_resource_id": "agent-remote-session-" + shortDigest(input.Snapshot.SessionID, 12) + ".service",
		"skill_snapshot_id":   input.Snapshot.SnapshotID, "task_record_id": input.Snapshot.TaskID,
		"runtime_uid":           12345,
		"workspace_remote_path": path.Join("/workspace", input.Snapshot.UserID, "workspaces", input.Session.WorkspaceID, "files"),
		"account_remote_path":   path.Join("/accounts", input.Snapshot.UserID, "tool-accounts", "claude", input.Snapshot.AccountID),
	}
	cases := []string{"valid", "extra", "relative_path", "unclean_path", "fractional_uid", "zero_uid", "oversize_uid"}
	for field := range original {
		cases = append(cases, field)
	}
	for _, field := range cases {
		t.Run(field, func(t *testing.T) {
			client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
				_, _ = bufio.NewReader(connection).ReadBytes('\n')
				result := make(map[string]any, len(original))
				for key, value := range original {
					result[key] = value
				}
				switch field {
				case "valid":
				case "relative_path":
					result["workspace_remote_path"] = strings.TrimPrefix(result["workspace_remote_path"].(string), "/")
				case "unclean_path":
					result["account_remote_path"] = "/elsewhere/../" + result["account_remote_path"].(string)
				case "fractional_uid":
					result["runtime_uid"] = 1.5
				case "zero_uid":
					result["runtime_uid"] = 0
				case "oversize_uid":
					result["runtime_uid"] = uint64(1) << 32
				default:
					result[field] = "wrong"
				}
				_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
			})
			_, err := client.StartManagedSession(context.Background(), "original-task", input)
			if (err == nil) != (field == "valid") {
				t.Fatal("unexpected launch receipt validation", err)
			}
		})
	}
}

func TestManagedLaunchRecoveryAndCancelValidateBoundedReceipts(t *testing.T) {
	for _, operation := range []string{managedRecoveryOperation, managedCancelOperation} {
		for _, field := range []string{"valid", "stopped", "status", "session_id", "skill_snapshot_id", "task_record_id", "runtime_backend", "extra", "missing"} {
			t.Run(operation+"/"+field, func(t *testing.T) {
				input := managedSpecClientInput(t)
				client := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
					_, _ = bufio.NewReader(connection).ReadBytes('\n')
					result := map[string]any{
						"status": "not_started", "session_id": input.Snapshot.SessionID,
						"skill_snapshot_id": input.Snapshot.SnapshotID, "task_record_id": input.Snapshot.TaskID, "runtime_backend": "native",
					}
					switch field {
					case "valid":
					case "stopped":
						result["status"] = "stopped"
					case "missing":
						delete(result, "task_record_id")
					default:
						result[field] = "wrong"
					}
					_ = json.NewEncoder(connection).Encode(Response{Version: ProtocolVersion, OK: true, Result: result})
				})
				var err error
				if operation == managedRecoveryOperation {
					_, err = client.RecoverManagedSession(context.Background(), "original-task", input)
				} else {
					err = client.CancelManagedSession(context.Background(), "original-task", input)
				}
				valid := field == "valid" || operation == managedCancelOperation && field == "stopped"
				if (err == nil) != valid {
					t.Fatal("unexpected bounded receipt validation", err)
				}
			})
		}
	}
}

func TestManagedLaunchInvocationRequiresTrustedService(t *testing.T) {
	spec := SessionSpec{Username: "ar-u-test", UnitName: "agent-remote-session-123456789abc.service"}
	original := "InvocationID=" + strings.Repeat("a", 32) + "\nTransient=yes\nUser=" + spec.Username + "\nControlGroup=/system.slice/" + spec.UnitName + "\n"
	for _, kind := range []string{"valid", "zero", "unknown_user", "not_transient", "group", "duplicate", "extra", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			output := original
			switch kind {
			case "zero":
				output = strings.Replace(output, strings.Repeat("a", 32), strings.Repeat("0", 32), 1)
			case "unknown_user":
				output = strings.Replace(output, spec.Username, "root", 1)
			case "not_transient":
				output = strings.Replace(output, "Transient=yes", "Transient=no", 1)
			case "group":
				output = strings.Replace(output, "/system.slice/", "/other.slice/", 1)
			case "duplicate":
				output += "User=" + spec.Username + "\n"
			case "extra":
				output += "Unknown=yes\n"
			case "oversize":
				output += strings.Repeat("x", 5000)
			}
			engine := NewEngine(EngineConfig{SystemctlPath: writeTestCommand(t, "systemctl", "cat <<'FIELDS'\n"+output+"FIELDS\n")})
			id, err := engine.managedLaunchInvocation(context.Background(), spec)
			if kind == "valid" {
				if err != nil || id != strings.Repeat("a", 32) {
					t.Fatal(id, err)
				}
			} else if err == nil {
				t.Fatal("foreign invocation accepted")
			}
		})
	}
}

func TestManagedLaunchSocketCancellationReleasesPendingLock(t *testing.T) {
	server := NewServer("", -1, -1, NewEngine(EngineConfig{}))
	server.mu.Lock()
	defer server.mu.Unlock()
	entered, finished := make(chan struct{}), make(chan struct{})
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) {
		reader := bufio.NewReader(connection)
		data, err := readBoundedLine(reader, maxHelperRequestBytes)
		if err != nil {
			t.Error(err)
			close(finished)
			return
		}
		var request Request
		if err := json.Unmarshal(data, &request); err != nil {
			t.Error(err)
		}
		close(entered)
		server.handleManagedSpec(ctx, connection, reader, request)
		close(finished)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.StartManagedSession(ctx, "original-task", managedSpecClientInput(t))
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not received")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("launch client ignored cancellation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled launch kept waiting for mutation lock")
	}
}
