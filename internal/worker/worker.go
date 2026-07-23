package worker

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/browser"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	noderuntime "github.com/Agent-Remote/agent-remote-node/internal/runtime"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/sshkeys"
	"github.com/Agent-Remote/agent-remote-node/internal/toolaccounts"
	"github.com/Agent-Remote/agent-remote-node/internal/toolsessions"
	"github.com/Agent-Remote/agent-remote-node/internal/workspace"
)

// Worker executes node heartbeats, task polling, and reconciliation.
type Worker struct {
	cfg    config.Config
	client api.Client
	ledger *ledger.Ledger
}

// New creates a Worker.
func New(cfg config.Config, client api.Client, taskLedger *ledger.Ledger) Worker {
	return Worker{cfg: cfg.WithDefaults(), client: client, ledger: taskLedger}
}

// Heartbeat sends a single heartbeat.
func (w Worker) Heartbeat(ctx context.Context) error {
	if w.cfg.WireGuardPublicKey != "" {
		peers, err := w.client.ListWireGuardPeers(ctx)
		if err != nil {
			return err
		}
		if _, err := runtimehelper.NewClient(w.cfg.RuntimeSocketPath).Call(
			ctx,
			fmt.Sprintf("wireguard-sync-%d", time.Now().UnixNano()),
			"wireguard_sync",
			map[string]any{"peers": peers.Data.Items},
		); err != nil {
			return err
		}
	}
	resources, runtimeStatus := noderuntime.Snapshot(w.cfg.AllowedRuntimeBackends, w.cfg.RuntimeSocketPath)
	return w.client.SendHeartbeat(ctx, api.HeartbeatRequest{
		NodeID:             w.cfg.NodeID,
		Version:            w.cfg.Version,
		SupportedToolTypes: w.cfg.SupportedToolTypes,
		WireGuardIP:        w.cfg.WireGuardIP(),
		WireGuardPublicKey: w.cfg.WireGuardPublicKey,
		WireGuardEndpoint:  w.cfg.WireGuardEndpoint,
		Resources:          resources,
		Runtime:            runtimeStatus,
	})
}

// PollOnce leases and executes one batch of tasks.
func (w Worker) PollOnce(ctx context.Context) error {
	response, err := w.client.PollTasks(ctx)
	if err != nil {
		return err
	}
	for _, task := range response.Data.Tasks {
		if err := w.executeTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

// Reconcile submits a basic node snapshot.
func (w Worker) Reconcile(ctx context.Context) error {
	resources, runtimeStatus := noderuntime.Snapshot(w.cfg.AllowedRuntimeBackends, w.cfg.RuntimeSocketPath)
	sessions := []any{}
	if slices.Contains(w.cfg.AllowedRuntimeBackends, "native") {
		result, err := runtimehelper.NewClient(w.cfg.RuntimeSocketPath).Call(
			ctx, fmt.Sprintf("reconcile-%d", time.Now().UnixNano()), "list_sessions", map[string]any{},
		)
		if err != nil {
			return err
		}
		if listed, ok := result["sessions"].([]any); ok {
			sessions = listed
		}
	}
	return w.client.Reconcile(ctx, api.ReconcileRequest{
		NodeID:   w.cfg.NodeID,
		Sections: []string{"runtime_sessions", "resources"},
		Snapshot: map[string]any{
			"resources": resources,
			"runtime":   runtimeStatus,
			"sessions":  sessions,
		},
	})
}

// Run starts the long-running node loop.
func (w Worker) Run(ctx context.Context) error {
	heartbeatTicker := time.NewTicker(time.Duration(w.cfg.HeartbeatIntervalSeconds) * time.Second)
	pollTicker := time.NewTicker(time.Duration(w.cfg.PollIntervalSeconds) * time.Second)
	defer heartbeatTicker.Stop()
	defer pollTicker.Stop()

	if err := w.Heartbeat(ctx); err != nil {
		return err
	}
	if err := w.Reconcile(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeatTicker.C:
			if err := w.Heartbeat(ctx); err != nil {
				return err
			}
		case <-pollTicker.C:
			if err := w.PollOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func (w Worker) executeTask(ctx context.Context, task api.TaskEnvelope) error {
	if entry, ok := w.ledger.Get(task.TaskID); ok && entry.Status == "succeeded" {
		return w.client.CompleteTask(ctx, task.TaskID, entry.Result)
	}

	if err := w.client.StartTask(ctx, task.TaskID); err != nil {
		return err
	}
	result, err := w.executeKnownTask(ctx, task)
	if err != nil {
		taskError := map[string]any{"code": "NODE_TASK_FAILED", "message": err.Error()}
		_ = w.ledger.Save(ledger.Entry{TaskID: task.TaskID, Status: "failed", Error: taskError})
		return w.client.FailTask(ctx, task.TaskID, taskError)
	}
	if err := w.ledger.Save(ledger.Entry{TaskID: task.TaskID, Status: "succeeded", Result: result}); err != nil {
		return err
	}
	return w.client.CompleteTask(ctx, task.TaskID, result)
}

func (w Worker) executeKnownTask(ctx context.Context, task api.TaskEnvelope) (map[string]any, error) {
	switch task.TaskType {
	case "reconcile_state":
		resources, runtimeStatus := noderuntime.Snapshot(w.cfg.AllowedRuntimeBackends, w.cfg.RuntimeSocketPath)
		result := map[string]any{
			"status":    "reconciled",
			"resources": resources,
			"runtime":   runtimeStatus,
			"sessions":  []any{},
		}
		if slices.Contains(w.cfg.AllowedRuntimeBackends, "native") {
			listed, err := w.callRuntimeHelper(ctx, task, "list_sessions", map[string]any{})
			if err != nil {
				return nil, err
			}
			result["sessions"] = listed["sessions"]
		}
		return result, nil
	case "migrate_tool_account_runtime":
		return w.callRuntimeHelper(ctx, task, "migrate_account", task.Payload)
	case "sync_ssh_keys":
		payload, err := sshkeys.DecodePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		path := w.cfg.SSHAuthorizedKeysPath
		if payload.AuthorizedKeysPath != nil && *payload.AuthorizedKeysPath != "" {
			path = *payload.AuthorizedKeysPath
		}
		if err := sshkeys.Sync(path, w.cfg.AttachBinaryPath, w.cfg.SourcePath, payload); err != nil {
			return nil, err
		}
		return map[string]any{"status": "synced", "authorized_keys_path": path}, nil
	case "prepare_workspace":
		payload, err := workspace.DecodePreparePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		sshPayload, err := sshkeys.DecodePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		if len(sshPayload.SSHKeys) > 0 {
			if err := sshkeys.Sync(w.cfg.SSHAuthorizedKeysPath, w.cfg.AttachBinaryPath, w.cfg.SourcePath, sshPayload); err != nil {
				return nil, err
			}
		}
		return w.callRuntimeHelper(ctx, task, "prepare_workspace", payload)
	case "create_binding_session":
		payload, err := toolaccounts.DecodeCreateBindingPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		if err := w.requireBackend(payload.RuntimeBackend); err != nil {
			return nil, err
		}
		operation := "docker_prepare_account"
		if payload.RuntimeBackend == "native" {
			operation = "prepare_account"
		}
		return w.callRuntimeHelper(ctx, task, operation, payload)
	case "verify_tool_account":
		payload, err := toolaccounts.DecodeVerifyPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		result, err := toolaccounts.Verify(w.cfg.AccountRoot, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"verified":            result.Verified,
			"tool_account_id":     result.ToolAccountID,
			"tool_type":           result.ToolType,
			"account_remote_path": result.AccountRemotePath,
			"metadata":            result.Metadata,
			"error":               result.Error,
		}, nil
	case "import_tool_account_config":
		payload, err := toolaccounts.DecodeImportConfigPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		result, err := toolaccounts.ImportConfig(w.cfg.AccountRoot, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"status":              result.Status,
			"tool_account_id":     result.ToolAccountID,
			"tool_type":           result.ToolType,
			"account_remote_path": result.AccountRemotePath,
			"files_written":       result.FilesWritten,
			"files_written_count": len(result.FilesWritten),
		}, nil
	case "create_tool_session":
		payload, err := toolsessions.DecodeCreatePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		if err := w.requireBackend(payload.RuntimeBackend); err != nil {
			return nil, err
		}
		operation := "docker_start_session"
		if payload.RuntimeBackend == "native" {
			operation = "start_session"
		}
		return w.callRuntimeHelper(ctx, task, operation, payload)
	case "stop_tool_session":
		payload, err := toolsessions.DecodeStopPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		if err := w.requireBackend(payload.RuntimeBackend); err != nil {
			return nil, err
		}
		operation := "docker_stop_session"
		if payload.RuntimeBackend == "native" {
			operation = "stop_session"
		}
		return w.callRuntimeHelper(ctx, task, operation, payload)
	case "create_browser_session":
		payload, err := browser.DecodeCreatePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		return w.callRuntimeHelper(ctx, task, "docker_start_browser", payload)
	case "stop_browser_session":
		payload, err := browser.DecodeStopPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		return w.callRuntimeHelper(ctx, task, "docker_stop_browser", payload)
	case "cleanup_resources":
		return w.callRuntimeHelper(ctx, task, "cleanup_resources", task.Payload)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedTask, task.TaskType)
	}
}

func (w Worker) requireBackend(backend string) error {
	if backend != "docker_sandbox" && backend != "native" {
		return fmt.Errorf("unsupported runtime backend %q", backend)
	}
	if !slices.Contains(w.cfg.AllowedRuntimeBackends, backend) {
		return fmt.Errorf("runtime backend %q is not enabled on this node", backend)
	}
	return nil
}

func (w Worker) callRuntimeHelper(ctx context.Context, task api.TaskEnvelope, operation string, payload any) (map[string]any, error) {
	mapped, err := runtimehelper.Map(payload)
	if err != nil {
		return nil, err
	}
	mapped["workspace_root"] = w.cfg.WorkspaceRoot
	mapped["account_root"] = w.cfg.AccountRoot
	mapped["claude_runtime_path"] = w.cfg.ClaudeRuntimePath
	return runtimehelper.NewClient(w.cfg.RuntimeSocketPath).Call(ctx, task.TaskID, operation, mapped)
}

// ErrUnsupportedTask identifies a task type this node does not implement.
var ErrUnsupportedTask = fmt.Errorf("unsupported task")
