package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

type finalizationTransferClient interface {
	ObserveSkillTermination(context.Context, skillmanager.FinalizationRecord) error
	BeginSkillFinalization(context.Context, api.SkillFinalizationInput, skillmanager.Manifest, *api.SkillFinalization) (api.SkillFinalization, error)
	GetSkillFinalization(context.Context, api.SkillFinalizationInput, api.SkillFinalization) (api.SkillFinalization, error)
	PutSkillFinalizationFile(context.Context, api.SkillFinalizationInput, api.SkillFinalization, skillmanager.Entry, io.ReadCloser) error
	CompleteSkillFinalization(context.Context, api.SkillFinalizationInput, api.SkillFinalization) (api.SkillFinalization, error)
	PublishSkillFinalization(context.Context, api.SkillFinalizationInput, api.SkillFinalization) (api.SkillPublication, error)
}

type finalizationReader interface {
	HoldSkillFinalization(context.Context, string, skillmanager.SnapshotBinding) (*os.File, skillmanager.FinalizationRecord, error)
	ReadSkillFinalization(context.Context, string, skillmanager.SnapshotBinding) (skillmanager.FinalizationRecord, skillmanager.Manifest, error)
	OpenSkillFinalizationObjects(context.Context, string, skillmanager.FinalizationRecord) (skillmanager.FrozenObjectReader, error)
	AcknowledgeSkillFinalization(context.Context, string, skillmanager.FinalizationAcknowledgement) (skillmanager.FinalizationRecord, error)
	CleanupFinalizedSkillSession(context.Context, string, skillmanager.FinalizationRecord) (skillmanager.FinalizationRecord, error)
}

// transferFinalization resumes a single original capture. Each verified response is persisted
// before the next mutation; an unknown result is inspected or replayed with the same identity.
// Neither file acknowledgements nor publication receipts authorize local content removal here.
func transferFinalization(ctx context.Context, client finalizationTransferClient, helper finalizationReader, journal finalizationTransferJournal, capture skillmanager.FinalizationRecord) error {
	if err := capture.Validate(); err != nil {
		return err
	}
	if capture.ObjectsVersion != 1 {
		return errors.New("finalization requires frozen objects")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	previous, err := journal.load(capture)
	if err != nil {
		return err
	}
	if previous == nil {
		previous, err = journal.save(nil, finalizationTransferRecord{SchemaVersion: 1, Capture: capture})
		if err != nil {
			return err
		}
	}
	if previous.record.Publication != nil {
		return acknowledgeTransferredFinalization(ctx, helper, previous.record)
	}
	if previous.record.Receipt == nil {
		if err := client.ObserveSkillTermination(ctx, capture); err != nil {
			return err
		}
	}
	input := finalizationInput(capture)
	if previous.record.Receipt != nil {
		view, err := client.GetSkillFinalization(ctx, input, *previous.record.Receipt)
		if err != nil {
			return err
		}
		previous, err = saveFinalizationReceipt(journal, previous, view)
		if err != nil {
			return err
		}
		if err := acknowledgeTransferProgress(ctx, helper, previous.record, capture); err != nil {
			return err
		}
	}
	if previous.record.Receipt == nil || previous.record.Receipt.CheckpointID == nil {
		hold, held, err := helper.HoldSkillFinalization(ctx, "finalization-hold:"+input.SnapshotID, capture.Binding)
		if err != nil {
			return err
		}
		if hold == nil {
			return errors.New("finalization reader omitted its content hold")
		}
		defer hold.Close()
		if !sameFinalizationCapture(held, capture) {
			return errors.New("finalization hold changed original capture")
		}
		original, manifest, err := helper.ReadSkillFinalization(ctx, "finalization-manifest:"+input.SnapshotID, capture.Binding)
		if err != nil {
			return err
		}
		digest, err := skillmanager.Digest(manifest)
		if err != nil || !sameFinalizationCapture(original, capture) || digest != capture.TreeDigest {
			return errors.New("finalization reader changed frozen input")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		view, err := client.BeginSkillFinalization(ctx, input, manifest, previous.record.Receipt)
		if err != nil {
			return err
		}
		previous, err = saveFinalizationReceipt(journal, previous, view)
		if err != nil {
			return err
		}
		if err := acknowledgeTransferProgress(ctx, helper, previous.record, capture); err != nil {
			return err
		}
		if view.CheckpointID == nil {
			if err := uploadFinalizationObjects(ctx, client, helper, capture, manifest, view); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			completed, err := client.CompleteSkillFinalization(ctx, input, view)
			if err != nil {
				return err
			}
			if completed.CheckpointID == nil || completed.UploadID != view.UploadID || completed.UploadAttempt != view.UploadAttempt {
				return errors.New("finalization completion lost original upload")
			}
			previous, err = saveFinalizationReceipt(journal, previous, completed)
			if err != nil {
				return err
			}
			if err := acknowledgeTransferProgress(ctx, helper, previous.record, capture); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publication, err := client.PublishSkillFinalization(ctx, input, *previous.record.Receipt)
	if err != nil {
		return err
	}
	record := previous.record
	record.Publication = &publication
	if _, err := journal.save(previous, record); err != nil {
		return err
	}
	return acknowledgeTransferredFinalization(ctx, helper, record)
}

func acknowledgeTransferProgress(ctx context.Context, helper finalizationReader, record finalizationTransferRecord, current skillmanager.FinalizationRecord) error {
	// A restored/missing worker ledger may lag the root journal. Retrieve the full original
	// publication before replaying its acknowledgement; never send a lower phase to the Helper.
	if current.CanDeleteSession() {
		return nil
	}
	return acknowledgeTransferredFinalization(ctx, helper, record)
}

func acknowledgeTransferredFinalization(ctx context.Context, helper finalizationReader, record finalizationTransferRecord) error {
	if record.Receipt == nil {
		return errors.New("cannot acknowledge absent Server receipt")
	}
	ack := skillmanager.FinalizationAcknowledgement{Version: 1, Capture: record.Capture, Receipt: *record.Receipt, Publication: record.Publication}
	target, err := ack.State()
	if err != nil {
		return err
	}
	confirmed, err := helper.AcknowledgeSkillFinalization(ctx, "finalization-ack:"+record.Capture.Binding.SnapshotID, ack)
	if err != nil {
		return err
	}
	if confirmed.Validate() != nil || !skillmanager.SameFinalizationInput(confirmed, record.Capture) || confirmed.State != target {
		return errors.New("Helper acknowledged a different finalization")
	}
	if record.Publication != nil {
		if err := finalizationPublicationOutcome(*record.Publication); err != nil {
			return err
		}
		cleaned, err := helper.CleanupFinalizedSkillSession(ctx, "finalization-cleanup:"+record.Capture.Binding.SnapshotID, confirmed)
		if err != nil {
			return err
		}
		if cleaned != confirmed {
			return errors.New("Helper cleanup changed original acknowledged finalization")
		}
	}
	return nil
}

func saveFinalizationReceipt(journal finalizationTransferJournal, previous *finalizationTransferEntry, receipt api.SkillFinalization) (*finalizationTransferEntry, error) {
	record := previous.record
	record.Receipt = &receipt
	return journal.save(previous, record)
}

func finalizationPublicationOutcome(publication api.SkillPublication) error {
	if publication.Status == "superseded" {
		return errors.New("retained finalization publication was superseded")
	}
	return nil
}

func uploadFinalizationObjects(ctx context.Context, client finalizationTransferClient, helper finalizationReader, capture skillmanager.FinalizationRecord, manifest skillmanager.Manifest, upload api.SkillFinalization) error {
	objects, err := openFinalizationObjectSource(ctx, helper, capture, time.Now)
	if err != nil {
		return err
	}
	defer objects.Close()
	seen := make(map[string]bool)
	input := finalizationInput(capture)
	for _, entry := range manifest.Entries {
		if entry.Kind != "file" || seen[entry.SHA256] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		file, actual, err := objects.Open(ctx, entry.SHA256)
		if err != nil {
			return err
		}
		if file == nil || actual != entry {
			if file != nil {
				_ = file.Close()
			}
			return errors.New("finalization object differs from frozen manifest")
		}
		err = client.PutSkillFinalizationFile(ctx, input, upload, entry, file)
		// The transport owns closure, but explicitly close on every outcome for test/adapter safety.
		_ = file.Close()
		if err != nil {
			return err
		}
		seen[entry.SHA256] = true
	}
	return nil
}
