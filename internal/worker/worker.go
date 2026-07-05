package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	noderuntime "github.com/Agent-Remote/agent-remote-node/internal/runtime"
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
	resources, runtimeStatus := noderuntime.Snapshot()
	return w.client.SendHeartbeat(ctx, api.HeartbeatRequest{
		NodeID:             w.cfg.NodeID,
		Version:            w.cfg.Version,
		SupportedToolTypes: w.cfg.SupportedToolTypes,
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
	resources, runtimeStatus := noderuntime.Snapshot()
	return w.client.Reconcile(ctx, api.ReconcileRequest{
		NodeID:   w.cfg.NodeID,
		Sections: []string{"containers", "tmux"},
		Snapshot: map[string]any{
			"resources": resources,
			"runtime":   runtimeStatus,
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
	result, err := w.executeKnownTask(task)
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

func (w Worker) executeKnownTask(task api.TaskEnvelope) (map[string]any, error) {
	switch task.TaskType {
	case "reconcile_state":
		return map[string]any{"status": "reconciled"}, nil
	case "sync_ssh_keys":
		payload, err := sshkeys.DecodePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		path := w.cfg.SSHAuthorizedKeysPath
		if payload.AuthorizedKeysPath != nil && *payload.AuthorizedKeysPath != "" {
			path = *payload.AuthorizedKeysPath
		}
		if err := sshkeys.Sync(path, w.cfg.AttachBinaryPath, payload); err != nil {
			return nil, err
		}
		return map[string]any{"status": "synced", "authorized_keys_path": path}, nil
	case "prepare_workspace":
		payload, err := workspace.DecodePreparePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		result, err := workspace.Prepare(w.cfg.WorkspaceRoot, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"status":      "prepared",
			"remote_path": result.RemotePath,
			"marker_path": result.MarkerPath,
		}, nil
	case "create_binding_session":
		payload, err := toolaccounts.DecodeCreateBindingPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		result, err := toolaccounts.PrepareBinding(w.cfg.AccountRoot, w.cfg.DockerBinaryPath, w.cfg.TmuxBinaryPath, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"status":              result.Status,
			"binding_session_id":  result.BindingSessionID,
			"tool_account_id":     result.ToolAccountID,
			"tool_type":           result.ToolType,
			"account_remote_path": result.AccountRemotePath,
			"tmux_session_name":   result.TmuxSessionName,
			"container_name":      result.ContainerName,
			"marker_path":         result.MarkerPath,
			"tmux_started":        result.TmuxStarted,
			"verifier":            result.Verifier,
		}, nil
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
	case "create_tool_session":
		payload, err := toolsessions.DecodeCreatePayload(task.Payload)
		if err != nil {
			return nil, err
		}
		result, err := toolsessions.Prepare(w.cfg.WorkspaceRoot, w.cfg.AccountRoot, w.cfg.DockerBinaryPath, w.cfg.TmuxBinaryPath, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"status":                result.Status,
			"session_id":            result.SessionID,
			"tool_account_id":       result.ToolAccountID,
			"tool_type":             result.ToolType,
			"workspace_remote_path": result.WorkspaceRemotePath,
			"account_remote_path":   result.AccountRemotePath,
			"tmux_session_name":     result.TmuxSessionName,
			"sandbox_name":          result.SandboxName,
			"container_id":          result.SandboxName,
			"marker_path":           result.MarkerPath,
			"tmux_started":          result.TmuxStarted,
		}, nil
	case "stop_tool_session":
		payload, err := toolsessions.DecodeStopPayload(task.Payload)
		if err != nil {
			return nil, err
		}
		result, err := toolsessions.Stop(w.cfg.DockerBinaryPath, w.cfg.TmuxBinaryPath, payload)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"status":            result.Status,
			"session_id":        result.SessionID,
			"tmux_session_name": result.TmuxSessionName,
			"sandbox_name":      result.SandboxName,
			"container_id":      result.SandboxName,
			"tmux_stopped":      result.TmuxStopped,
			"sandbox_removed":   result.SandboxRemoved,
		}, nil
	case "cleanup_resources":
		return map[string]any{"status": "cleaned"}, nil
	default:
		return map[string]any{"status": "noop", "task_type": task.TaskType}, nil
	}
}

// ErrUnsupportedTask is reserved for future strict task execution.
var ErrUnsupportedTask = fmt.Errorf("unsupported task")
