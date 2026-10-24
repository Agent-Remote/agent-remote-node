package runtime

import (
	"context"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

// ProbeSessionAttach checks the installed gateway independently of binding support.
func ProbeSessionAttach(ctx context.Context, binaryPath string) api.SessionAttachCapability {
	capability := probeAttachProtocol(ctx, binaryPath, "--session-protocol-info", 2*time.Second)
	return api.SessionAttachCapability{Supported: capability.Supported, ProtocolVersion: capability.ProtocolVersion}
}

// ProbeSessionAttachmentTransfer checks the separately installed transfer
// gateway. A terminal gateway alone never enables file or image transfer.
func ProbeSessionAttachmentTransfer(ctx context.Context, binaryPath string) api.SessionAttachmentTransferCapability {
	capability := probeAttachProtocol(ctx, binaryPath, "--session-attachments-protocol-info", 2*time.Second)
	if !capability.Supported || capability.ProtocolVersion != 1 {
		return api.SessionAttachmentTransferCapability{}
	}
	return api.SessionAttachmentTransferCapability{Supported: true, ProtocolVersion: 1, MaxBytes: 256 * 1024 * 1024, MaxEntries: 32}
}
