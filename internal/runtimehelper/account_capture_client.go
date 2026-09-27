package runtimehelper

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

// ReadAccountCapture reads and verifies the retained manifest through a scoped read-only descriptor.
func (c Client) ReadAccountCapture(ctx context.Context, requestID string, binding skillmanager.AccountTakeoverBinding) (skillmanager.AccountCapture, skillmanager.Manifest, error) {
	file, metadata, err := c.openCaptureFile(ctx, requestID, accountCaptureFileRequest{Binding: binding, Kind: "manifest"})
	if err != nil {
		return skillmanager.AccountCapture{}, skillmanager.Manifest{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCaptureManifestBytes+1))
	if err != nil {
		return skillmanager.AccountCapture{}, skillmanager.Manifest{}, err
	}
	if int64(len(data)) != metadata.Size || len(data) > maxCaptureManifestBytes {
		return skillmanager.AccountCapture{}, skillmanager.Manifest{}, errors.New("retained capture manifest length changed")
	}
	manifest, err := skillmanager.DecodeManifest(data)
	if err != nil {
		return skillmanager.AccountCapture{}, skillmanager.Manifest{}, err
	}
	digest, err := skillmanager.Digest(manifest)
	if err != nil || digest != metadata.Capture.TreeDigest || !metadata.Capture.SourceExists && len(manifest.Entries) != 0 {
		return skillmanager.AccountCapture{}, skillmanager.Manifest{}, errors.New("retained capture manifest identity changed")
	}
	if err := ctx.Err(); err != nil {
		return skillmanager.AccountCapture{}, skillmanager.Manifest{}, err
	}
	return metadata.Capture, manifest, nil
}

// OpenAccountCaptureObject transfers one manifest-scoped file; the caller owns closure and byte verification.
func (c Client) OpenAccountCaptureObject(ctx context.Context, requestID string, capture skillmanager.AccountCapture, digest string) (*os.File, skillmanager.Entry, error) {
	if err := capture.Validate(); err != nil {
		return nil, skillmanager.Entry{}, err
	}
	file, metadata, err := c.openCaptureFile(ctx, requestID, accountCaptureFileRequest{
		Binding: capture.Binding, Kind: "object", Digest: digest, HelperReceiptID: capture.HelperReceiptID, TreeDigest: capture.TreeDigest,
	})
	if err != nil {
		return nil, skillmanager.Entry{}, err
	}
	if metadata.Capture != capture || metadata.Entry == nil {
		_ = file.Close()
		return nil, skillmanager.Entry{}, errors.New("retained capture object belongs to another receipt")
	}
	return file, *metadata.Entry, nil
}

func (c Client) openCaptureFile(ctx context.Context, requestID string, payload accountCaptureFileRequest) (*os.File, accountCaptureFileResponse, error) {
	if err := validateID(requestID, "request_id"); err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	if err := payload.validate(); err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	mapped, err := Map(payload)
	if err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	request := Request{Version: ProtocolVersion, RequestID: requestID, Operation: accountCaptureFileOperation, Payload: mapped}
	var metadata accountCaptureFileResponse
	file, err := c.openRetainedFile(ctx, request, func(data []byte, count int) (int64, error) {
		var err error
		metadata, err = decodeCaptureFileFrame(data, count, payload)
		return metadata.Size, err
	})
	return file, metadata, err
}

func decodeCaptureFileFrame(data []byte, descriptorCount int, expected accountCaptureFileRequest) (accountCaptureFileResponse, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return accountCaptureFileResponse{}, err
	}
	var response struct {
		Version int                        `json:"version"`
		OK      bool                       `json:"ok"`
		Result  accountCaptureFileResponse `json:"result"`
		Error   *Error                     `json:"error,omitempty"`
	}
	if err := decodeStrictJSON(data, &response); err != nil {
		return accountCaptureFileResponse{}, err
	}
	if response.Version != ProtocolVersion {
		return accountCaptureFileResponse{}, errors.New("unsupported capture response protocol")
	}
	if !response.OK {
		if descriptorCount != 0 || response.Error == nil {
			return accountCaptureFileResponse{}, errors.New("invalid capture failure response")
		}
		return accountCaptureFileResponse{}, &Error{Code: "CAPTURE_UNAVAILABLE", Message: "Retained account capture is unavailable."}
	}
	if descriptorCount != 1 || response.Error != nil {
		return accountCaptureFileResponse{}, errors.New("capture response lacks its unique descriptor")
	}
	metadata := response.Result
	if err := validateCaptureFileResponse(metadata, expected); err != nil {
		return metadata, err
	}
	return metadata, nil
}

func validateCaptureFileResponse(metadata accountCaptureFileResponse, expected accountCaptureFileRequest) error {
	if err := metadata.Capture.Validate(); err != nil {
		return err
	}
	if metadata.Capture.Binding != expected.Binding || metadata.Kind != expected.Kind || metadata.Size < 0 {
		return errors.New("capture descriptor binding changed")
	}
	if expected.Kind == "manifest" {
		if metadata.Size == 0 || metadata.Size > maxCaptureManifestBytes || metadata.Entry != nil {
			return errors.New("invalid capture manifest metadata")
		}
		return nil
	}
	if metadata.Capture.HelperReceiptID != expected.HelperReceiptID || metadata.Capture.TreeDigest != expected.TreeDigest || metadata.Entry == nil || metadata.Entry.SHA256 != expected.Digest || metadata.Entry.Size != metadata.Size {
		return errors.New("capture descriptor object identity changed")
	}
	_, err := skillmanager.NewContentVerifier(*metadata.Entry)
	return err
}
