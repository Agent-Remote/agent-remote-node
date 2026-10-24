package runtime

import (
	"context"
	"testing"
)

func TestSessionAttachProbeIsIndependentAndStrict(t *testing.T) {
	for _, test := range []struct {
		name, flag, output string
		supported          bool
	}{
		{"exact", "--session-protocol-info", `{"supported":true,"protocol_version":1}`, true},
		{"binding_only", "--protocol-info", `{"supported":true,"protocol_version":1}`, false},
		{"duplicate", "--session-protocol-info", `{"supported":false,"supported":true,"protocol_version":1}`, false},
		{"alias", "--session-protocol-info", `{"Supported":true,"protocol_version":1}`, false},
		{"missing", "--session-protocol-info", `{"supported":true}`, false},
		{"null", "--session-protocol-info", `{"supported":true,"protocol_version":null}`, false},
		{"future", "--session-protocol-info", `{"supported":true,"protocol_version":2}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := bindingProbeScript(t, "test \"$#\" = 1 && test \"$1\" = "+test.flag+" || exit 19\nprintf '%s' '"+test.output+"'\n")
			got := ProbeSessionAttach(context.Background(), path)
			if got.Supported != test.supported {
				t.Fatalf("capability: %#v", got)
			}
		})
	}
}

func TestSessionAttachmentTransferProbeRequiresDedicatedGateway(t *testing.T) {
	path := bindingProbeScript(t, "test \"$#\" = 1 && test \"$1\" = --session-attachments-protocol-info || exit 19\nprintf '%s' '{\"supported\":true,\"protocol_version\":1}'\n")
	got := ProbeSessionAttachmentTransfer(context.Background(), path)
	if !got.Supported || got.ProtocolVersion != 1 || got.MaxBytes != 256*1024*1024 || got.MaxEntries != 32 {
		t.Fatalf("unexpected transfer capability: %#v", got)
	}
}
