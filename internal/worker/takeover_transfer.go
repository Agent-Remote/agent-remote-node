package worker

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type takeoverTransferClient interface {
	takeoverLeaseClient
	GetSkillTakeover(context.Context, skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error)
	BeginSkillTakeover(context.Context, skillmanager.AccountCapture, skillmanager.Manifest) (api.SkillTakeover, error)
	PutSkillTakeoverFile(context.Context, skillmanager.AccountTakeoverBinding, string, skillmanager.Entry, io.ReadCloser) error
	CompleteSkillTakeover(context.Context, skillmanager.AccountCapture, string) (api.SkillTakeover, error)
}

type takeoverCaptureReader interface {
	ReadAccountCapture(context.Context, string, skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error)
	OpenAccountCaptureObject(context.Context, string, skillmanager.AccountCapture, string) (*os.File, skillmanager.Entry, error)
}

// transferRetainedTakeover consumes only an already sealed capture without initiating new work.
func (w Worker) transferRetainedTakeover(ctx context.Context, binding skillmanager.AccountTakeoverBinding, attempt int64) (api.SkillTakeover, error) {
	if binding.NodeID != w.cfg.NodeID {
		return api.SkillTakeover{}, errors.New("takeover belongs to another node")
	}
	return transferTakeoverCapture(ctx, w.client, runtimehelper.NewClient(w.cfg.RuntimeSocketPath), binding, attempt)
}

func transferTakeoverCapture(ctx context.Context, client takeoverTransferClient, helper takeoverCaptureReader, binding skillmanager.AccountTakeoverBinding, attempt int64) (api.SkillTakeover, error) {
	if err := binding.Validate(); err != nil {
		return api.SkillTakeover{}, err
	}
	grant, err := client.GetSkillTakeover(ctx, binding)
	if err != nil {
		return api.SkillTakeover{}, err
	}
	if grant.Status == "committed" {
		return grant, nil
	}
	var committed api.SkillTakeover
	err = withTakeoverLease(ctx, client, binding, attempt, func(workCtx context.Context) error {
		var err error
		committed, err = uploadTakeoverCapture(workCtx, client, helper, binding)
		return err
	})
	// A verified commit can race the renewer's terminal-task rejection; the receipt remains authoritative.
	if committed.Status == "committed" {
		return committed, nil
	}
	if err != nil {
		return api.SkillTakeover{}, err
	}
	return api.SkillTakeover{}, errors.New("takeover transfer ended without an authority receipt")
}

func uploadTakeoverCapture(workCtx context.Context, client takeoverTransferClient, helper takeoverCaptureReader, binding skillmanager.AccountTakeoverBinding) (api.SkillTakeover, error) {
	capture, manifest, err := helper.ReadAccountCapture(workCtx, "takeover-manifest:"+binding.TaskID, binding)
	if err != nil {
		return api.SkillTakeover{}, err
	}
	if capture.Binding != binding {
		return api.SkillTakeover{}, errors.New("retained capture belongs to another task")
	}
	upload, err := client.BeginSkillTakeover(workCtx, capture, manifest)
	if err != nil {
		return api.SkillTakeover{}, err
	}
	if upload.Status == "committed" {
		return upload, nil
	}
	if upload.Status != "uploading" || upload.UploadID == nil {
		return api.SkillTakeover{}, errors.New("takeover lacks its current upload")
	}
	seen := make(map[string]bool)
	for _, entry := range manifest.Entries {
		if entry.Kind != "file" || seen[entry.SHA256] {
			continue
		}
		if err := workCtx.Err(); err != nil {
			return api.SkillTakeover{}, err
		}
		file, actual, err := helper.OpenAccountCaptureObject(workCtx, "takeover-object:"+binding.TaskID, capture, entry.SHA256)
		if err != nil {
			return api.SkillTakeover{}, err
		}
		if file == nil || actual != entry {
			if file != nil {
				_ = file.Close()
			}
			return api.SkillTakeover{}, errors.New("retained object differs from its manifest")
		}
		if err := client.PutSkillTakeoverFile(workCtx, binding, *upload.UploadID, entry, file); err != nil {
			return api.SkillTakeover{}, err
		}
		seen[entry.SHA256] = true
	}
	completed, err := client.CompleteSkillTakeover(workCtx, capture, *upload.UploadID)
	if err != nil {
		return api.SkillTakeover{}, err
	}
	if completed.Status != "committed" || completed.CheckpointID == nil {
		return api.SkillTakeover{}, errors.New("takeover did not retain its authority receipt")
	}
	return completed, nil
}
