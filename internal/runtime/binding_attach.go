package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
)

// ProbeBindingAttach checks the installed gateway's exact binding and SSH key protocol.
func ProbeBindingAttach(ctx context.Context, binaryPath string) api.BindingAttachCapability {
	return probeBindingAttach(ctx, binaryPath, 2*time.Second)
}

func probeBindingAttach(ctx context.Context, binaryPath string, timeout time.Duration) api.BindingAttachCapability {
	return probeAttachProtocol(ctx, binaryPath, "--protocol-info", timeout)
}

func probeAttachProtocol(ctx context.Context, binaryPath, flag string, timeout time.Duration) api.BindingAttachCapability {
	unsupported := api.BindingAttachCapability{}
	path, err := exec.LookPath(binaryPath)
	if err != nil || !executableFile(path) {
		return unsupported
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, flag)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A failed installed wrapper must not leave children or open output pipes behind.
	command.Cancel = func() error {
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	command.WaitDelay = 100 * time.Millisecond
	output := &bindingProbeOutput{cancel: cancel}
	command.Stdout = output
	command.Stderr = io.Discard
	if command.Start() != nil {
		return unsupported
	}
	defer syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if command.Wait() != nil || ctx.Err() != nil || output.exceeded {
		return unsupported
	}
	decoder := json.NewDecoder(&output.buffer)
	var capability api.BindingAttachCapability
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return unsupported
	}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return unsupported
		}
		seen[name] = true
		switch name {
		case "supported":
			err = decoder.Decode(&capability.Supported)
		case "protocol_version":
			err = decoder.Decode(&capability.ProtocolVersion)
		default:
			return unsupported
		}
		if err != nil {
			return unsupported
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || !capability.Supported || capability.ProtocolVersion != 1 {
		return unsupported
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return unsupported
	}
	return capability
}

type bindingProbeOutput struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (output *bindingProbeOutput) Write(data []byte) (int, error) {
	if output.buffer.Len()+len(data) > 256 {
		output.exceeded = true
		output.cancel()
		return 0, io.ErrShortBuffer
	}
	return output.buffer.Write(data)
}
