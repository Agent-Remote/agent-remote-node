package runtimehelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// HoldSkillFinalization prevents content reclamation for the caller's entire read operation.
// Close the returned read-only descriptor only after all manifest/object reads and completion.
func (c Client) HoldSkillFinalization(ctx context.Context, requestID string, binding skillmanager.SnapshotBinding) (*os.File, skillmanager.FinalizationRecord, error) {
	file, metadata, err := c.openFinalizationFile(ctx, requestID, finalizationFileRequest{Binding: binding, Kind: "hold"})
	return file, metadata.Record, err
}

// ReadSkillFinalization reads the sealed original manifest without touching the stopped work tree.
func (c Client) ReadSkillFinalization(ctx context.Context, requestID string, binding skillmanager.SnapshotBinding) (skillmanager.FinalizationRecord, skillmanager.Manifest, error) {
	file, metadata, err := c.openFinalizationFile(ctx, requestID, finalizationFileRequest{Binding: binding, Kind: "manifest"})
	if err != nil {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCaptureManifestBytes+1))
	if err != nil || int64(len(data)) != metadata.Size || len(data) > maxCaptureManifestBytes {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, errors.New("retained finalization manifest length changed")
	}
	manifest, err := skillmanager.DecodeManifest(data)
	if err != nil {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, err
	}
	digest, err := skillmanager.Digest(manifest)
	if err != nil || digest != metadata.Record.TreeDigest {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, errors.New("retained finalization manifest identity changed")
	}
	if err := ctx.Err(); err != nil {
		return skillmanager.FinalizationRecord{}, skillmanager.Manifest{}, err
	}
	return metadata.Record, manifest, nil
}

// OpenSkillFinalizationObject transfers a read-only file from the exact frozen input.
// The caller owns descriptor closure and must verify bytes before acknowledging Server persistence.
func (c Client) OpenSkillFinalizationObject(ctx context.Context, requestID string, record skillmanager.FinalizationRecord, digest string) (*os.File, skillmanager.Entry, error) {
	if err := record.Validate(); err != nil {
		return nil, skillmanager.Entry{}, err
	}
	if record.ObjectsVersion != 1 {
		return nil, skillmanager.Entry{}, errors.New("finalization lacks retained objects")
	}
	file, metadata, err := c.openFinalizationFile(ctx, requestID, finalizationFileRequest{
		Binding: record.Binding, Kind: "object", Digest: digest, TreeDigest: record.TreeDigest, Unclean: &record.Unclean,
	})
	if err != nil {
		return nil, skillmanager.Entry{}, err
	}
	return file, *metadata.Entry, nil
}

func (c Client) openFinalizationFile(ctx context.Context, requestID string, payload finalizationFileRequest) (*os.File, finalizationFileResponse, error) {
	if err := validateID(requestID, "request_id"); err != nil {
		return nil, finalizationFileResponse{}, err
	}
	if err := payload.validate(); err != nil {
		return nil, finalizationFileResponse{}, err
	}
	mapped, err := Map(payload)
	if err != nil {
		return nil, finalizationFileResponse{}, err
	}
	request := Request{Version: ProtocolVersion, RequestID: requestID, Operation: finalizationFileOperation, Payload: mapped}
	var metadata finalizationFileResponse
	file, err := c.openRetainedFile(ctx, request, func(data []byte, count int) (int64, error) {
		var err error
		metadata, err = decodeFinalizationFileFrame(data, count, payload)
		return metadata.Size, err
	})
	return file, metadata, err
}

func decodeFinalizationFileFrame(data []byte, count int, expected finalizationFileRequest) (finalizationFileResponse, error) {
	var response struct {
		Version int                      `json:"version"`
		OK      bool                     `json:"ok"`
		Result  finalizationFileResponse `json:"result"`
		Error   *Error                   `json:"error,omitempty"`
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return finalizationFileResponse{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields["version"] == nil || fields["ok"] == nil {
		return finalizationFileResponse{}, errors.New("missing finalization protocol fields")
	}
	for name := range fields {
		if name != "version" && name != "ok" && name != "result" && name != "error" {
			return finalizationFileResponse{}, errors.New("unknown finalization protocol field")
		}
	}
	if err := decodeStrictJSON(data, &response); err != nil {
		return finalizationFileResponse{}, err
	}
	if response.Version != ProtocolVersion || !response.OK || response.Error != nil || count != 1 {
		return finalizationFileResponse{}, &Error{Code: "FINALIZATION_UNAVAILABLE", Message: "Retained skill finalization is unavailable."}
	}
	metadata := response.Result
	if err := metadata.Record.Validate(); err != nil {
		return metadata, err
	}
	if metadata.Record.ObjectsVersion != 1 || metadata.Record.Binding != expected.Binding || metadata.Kind != expected.Kind || metadata.Size < 0 {
		return metadata, errors.New("finalization descriptor binding changed")
	}
	if expected.Kind == "hold" {
		if metadata.Size <= 0 || metadata.Size > maxCaptureManifestBytes || metadata.Entry != nil {
			return metadata, errors.New("invalid finalization read hold")
		}
		return metadata, nil
	}
	if expected.Kind == "manifest" {
		if metadata.Size == 0 || metadata.Size > maxCaptureManifestBytes || metadata.Entry != nil {
			return metadata, errors.New("invalid finalization manifest metadata")
		}
		return metadata, nil
	}
	if expected.Unclean == nil || metadata.Record.Unclean != *expected.Unclean || metadata.Record.TreeDigest != expected.TreeDigest ||
		metadata.Entry == nil || metadata.Entry.SHA256 != expected.Digest || metadata.Entry.Size != metadata.Size {
		return metadata, errors.New("finalization descriptor object identity changed")
	}
	_, err := skillmanager.NewContentVerifier(*metadata.Entry)
	return metadata, err
}
