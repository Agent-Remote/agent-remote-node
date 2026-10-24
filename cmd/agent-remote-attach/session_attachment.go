package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/claudeattachments"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
)

// runExactAttachmentGateway performs strict admission before reading any
// frame. It never accepts an account path or falls back to the legacy receiver.
func runExactAttachmentGateway(cfg config.Config, sessionID, expectedDevice, deviceID, sshKeyID string) error {
	if sshKeyID == "" {
		return errors.New("SSH key is required for exact attachment transfer")
	}
	if err := validateCommandID(sessionID, "session ID"); err != nil {
		return err
	}
	if err := validateCommandID(expectedDevice, "expected device ID"); err != nil {
		return err
	}
	if deviceID != expectedDevice {
		return errors.New("SSH device differs from the reviewed session device")
	}
	leaseID := claudeattachments.NewLeaseID()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := api.NewClient(cfg.ServerURL, cfg.NodeToken)
	response, err := client.VerifyExactAttachment(ctx, api.VerifyExactAttachmentRequest{
		Version: 1, NodeID: cfg.NodeID, SessionID: sessionID, DeviceID: deviceID, SSHKeyID: sshKeyID, LeaseID: leaseID,
	})
	if err != nil {
		return err
	}
	// Resolve the prepared account only after Server admission. The mapping is
	// trusted local state and is never selected by the client or used to bypass
	// the lease's runtime identity.
	accountPath, err := claudeattachments.ResolveSessionAccount(cfg.AccountRoot, sessionID)
	if err != nil {
		return err
	}
	expires, err := time.Parse(time.RFC3339Nano, response.Data.ExpiresAt)
	if err != nil || !expires.After(time.Now().UTC()) {
		return errors.New("exact attachment lease is expired")
	}
	receiptRoot := filepath.Join(filepath.Dir(cfg.LedgerPath), "exact-attachment-receipts")
	gateway := claudeattachments.Gateway{
		Identity:    claudeattachments.TransferIdentity{NodeID: cfg.NodeID, SessionID: sessionID, ExpectedDeviceID: expectedDevice, SSHKeyID: sshKeyID, LeaseID: leaseID},
		AccountPath: accountPath, LeaseExpiresAt: expires, MaxBytes: response.Data.MaxBytes, MaxEntries: response.Data.MaxEntries, ReceiptRoot: receiptRoot,
		CleanupHook: func(receipt claudeattachments.CleanupReceipt) error {
			_, err := client.SubmitExactAttachmentCleanup(ctx, api.CleanupExactAttachmentRequest{
				Operation: "cleanup_receipt", Version: 1, NodeID: receipt.NodeID, SessionID: receipt.SessionID, DeviceID: deviceID, SSHKeyID: receipt.SSHKeyID,
				LeaseID: receipt.LeaseID, Status: string(receipt.Status), RemovedBatches: receipt.RemovedBatches, ReceiptID: receipt.ReceiptID,
			})
			return err
		},
	}
	return gateway.Serve(ctx, os.Stdin, os.Stdout)
}

func attachmentTargetFromOriginalCommand(command string) (sessionID, expectedDevice string, ok bool, err error) {
	if len(command) > 512 {
		return "", "", false, errors.New("SSH attachment command is invalid")
	}
	fields := stringsFields(command)
	if len(fields) != 7 || fields[0] != "agent-remote-attach" || fields[1] != "--session" || fields[3] != "--expected-device" || fields[5] != "--attachment-transfer" || fields[6] != "1" {
		return "", "", false, nil
	}
	if err := validateCommandID(fields[2], "session ID"); err != nil {
		return "", "", false, err
	}
	if err := validateCommandID(fields[4], "expected device ID"); err != nil {
		return "", "", false, err
	}
	return fields[2], fields[4], true, nil
}

func stringsFields(command string) []string {
	// SSH_ORIGINAL_COMMAND is intentionally accepted only as fixed words. The
	// caller still compares the expected device to the trusted forced command.
	return strings.Fields(command)
}
