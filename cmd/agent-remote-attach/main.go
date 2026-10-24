package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/portforward"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Probe only the installed gateway; this path must not load credentials or dispatch a runtime.
	if len(args) == 1 && (args[0] == "--protocol-info" || args[0] == "--session-protocol-info" || args[0] == "--session-attachments-protocol-info") {
		_, err := fmt.Fprintln(os.Stdout, `{"supported":true,"protocol_version":1}`)
		return err
	}
	fs := flag.NewFlagSet("agent-remote-attach", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath(), "config path")
	sessionID := fs.String("session", "", "session ID")
	bindingID := fs.String("binding", "", "tool account ID for a binding session")
	expectedBinding := fs.String("binding-session", "", "exact binding attempt ID")
	deviceID := fs.String("device", "", "device ID")
	sshKeyID := fs.String("ssh-key", "", "SSH key ID from the forced-command identity")
	dryRun := fs.Bool("dry-run", false, "print tmux command without executing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *deviceID == "" {
		return fmt.Errorf("device is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(true); err != nil {
		return err
	}
	if *sessionID != "" && *bindingID != "" {
		return fmt.Errorf("session and binding are mutually exclusive")
	}
	if *expectedBinding != "" && *bindingID == "" {
		return fmt.Errorf("binding-session requires a binding account")
	}
	if *expectedBinding != "" {
		if err := validateCommandID(*expectedBinding, "binding session ID"); err != nil {
			return err
		}
	}
	originalCommand := os.Getenv("SSH_ORIGINAL_COMMAND")
	if attachmentSession, attachmentDevice, attachmentOK, attachmentErr := attachmentTargetFromOriginalCommand(originalCommand); attachmentErr != nil {
		return attachmentErr
	} else if attachmentOK {
		return runExactAttachmentGateway(cfg, attachmentSession, attachmentDevice, *deviceID, *sshKeyID)
	} else if strings.HasPrefix(strings.TrimSpace(originalCommand), "agent-remote-attach --session ") && strings.Contains(originalCommand, "--attachment-transfer") {
		return errors.New("invalid exact attachment command")
	}
	if *sessionID == "" && *bindingID == "" {
		if strings.HasPrefix(strings.TrimSpace(originalCommand), "agent-remote-skill-export") {
			snapshotID, err := frozenExportFromCommand(originalCommand)
			if err != nil {
				return err
			}
			return runFrozenExport(cfg, *deviceID, *sshKeyID, snapshotID, *dryRun)
		}
		if forwardID, tunnelErr := tunnelFromOriginalCommand(originalCommand); tunnelErr == nil {
			if *sshKeyID == "" {
				return fmt.Errorf("SSH key is required for port forwarding")
			}
			return runTunnelGateway(cfg, *deviceID, *sshKeyID, forwardID, *dryRun)
		} else if strings.HasPrefix(strings.TrimSpace(originalCommand), "agent-remote-tunnel") {
			return tunnelErr
		}
	}
	targetKind := "session"
	expectedDevice := ""
	targetID := *sessionID
	if *bindingID != "" {
		targetKind, targetID = "binding", *bindingID
	}
	if targetID == "" {
		if target, parseErr := attachRequestFromOriginalCommand(originalCommand); parseErr == nil {
			targetKind, targetID, *expectedBinding = target.kind, target.id, target.bindingSession
			expectedDevice = target.expectedDevice
		} else {
			if strings.HasPrefix(strings.TrimSpace(originalCommand), "agent-remote-attach") {
				return parseErr
			}
			return runSyncGateway(cfg, *deviceID, *sshKeyID, originalCommand, *dryRun)
		}
	}
	client := api.NewClient(cfg.ServerURL, cfg.NodeToken)
	var runtimeBackend, runtimeSessionID, tmuxSessionName string
	var forwardSSHAgent bool
	if targetKind == "binding" {
		if *expectedBinding != "" && *sshKeyID == "" {
			return fmt.Errorf("SSH key is required for exact binding attachment")
		}
		response, err := client.VerifyBindingAttach(context.Background(), api.VerifyBindingAttachRequest{
			NodeID: cfg.NodeID, ToolAccountID: targetID, DeviceID: *deviceID,
			ExpectedBindingSessionID: *expectedBinding,
			SSHKeyID:                 *sshKeyID,
		})
		if err != nil {
			return err
		}
		// Older servers may ignore the additive expected-attempt field.
		if *expectedBinding != "" && response.Data.BindingSessionID != *expectedBinding {
			return fmt.Errorf("binding session changed; reconnect after reviewing the current binding")
		}
		if *expectedBinding != "" && response.Data.SSHKeyID != *sshKeyID {
			return fmt.Errorf("server did not verify the exact SSH key for binding attachment")
		}
		runtimeBackend = response.Data.RuntimeBackend
		runtimeSessionID = response.Data.BindingSessionID
		tmuxSessionName = response.Data.TmuxSessionName
	} else if expectedDevice != "" {
		response, err := verifyExactSession(cfg, client, targetID, expectedDevice, *deviceID, *sshKeyID)
		if err != nil {
			return err
		}
		runtimeBackend = response.Data.RuntimeBackend
		runtimeSessionID = response.Data.SessionID
		tmuxSessionName = response.Data.TmuxSessionName
		forwardSSHAgent = response.Data.ForwardSSHAgent
	} else {
		response, err := client.VerifyAttach(context.Background(), api.VerifyAttachRequest{
			NodeID: cfg.NodeID, SessionID: targetID, DeviceID: *deviceID,
		})
		if err != nil {
			return err
		}
		runtimeBackend = response.Data.RuntimeBackend
		runtimeSessionID = response.Data.SessionID
		tmuxSessionName = response.Data.TmuxSessionName
		forwardSSHAgent = response.Data.ForwardSSHAgent
	}
	command, argsForTmux, err := attachInvocation(
		cfg, targetKind, runtimeBackend, runtimeSessionID, tmuxSessionName,
		forwardSSHAgent, strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")),
	)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("%s %s\n", command, strings.Join(argsForTmux, " "))
		return nil
	}
	cmd := exec.Command(command, argsForTmux...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func attachInvocation(
	cfg config.Config,
	targetKind string,
	runtimeBackend string,
	runtimeSessionID string,
	tmuxSessionName string,
	forwardSSHAgent bool,
	sshAgentSocket string,
) (string, []string, error) {
	if runtimeBackend != "native" && runtimeBackend != "docker_sandbox" {
		return "", nil, fmt.Errorf("unsupported runtime backend %q", runtimeBackend)
	}
	arguments := []string{
		"-n", cfg.RuntimeBinaryPath, "attach", "--session", runtimeSessionID,
		"--runtime-backend", runtimeBackend,
	}
	if forwardSSHAgent && sshAgentSocket != "" {
		arguments = append(arguments, "--ssh-agent-sock", sshAgentSocket)
	}
	return "sudo", arguments, nil
}

func runTunnelGateway(cfg config.Config, deviceID string, sshKeyID string, forwardID string, dryRun bool) error {
	if dryRun {
		fmt.Printf("agent-remote tunnel %s\n", forwardID)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connection := &stdioConnection{reader: os.Stdin, writer: os.Stdout}
	return portforward.Serve(ctx, portforward.Config{
		ForwardID: forwardID, DeviceID: deviceID, SSHKeyID: sshKeyID,
		Client:     api.NewClient(cfg.ServerURL, cfg.NodeToken),
		Runtime:    runtimehelper.NewClient(cfg.RuntimeSocketPath),
		Connection: connection,
	})
}

func defaultConfigPath() string {
	if path := strings.TrimSpace(os.Getenv("AGENT_REMOTE_NODE_CONFIG")); path != "" {
		return path
	}
	const systemPath = "/etc/agent-remote-node/config.json"
	if _, err := os.Stat(systemPath); err == nil {
		return systemPath
	}
	return "config.json"
}

func runSyncGateway(cfg config.Config, deviceID string, sshKeyID string, originalCommand string, dryRun bool) error {
	if err := validateCommandID(sshKeyID, "SSH key ID"); err != nil {
		return err
	}
	if originalCommand == "" || len(originalCommand) > 4096 || strings.ContainsAny(originalCommand, "\x00\r\n") {
		return fmt.Errorf("SSH sync command is invalid")
	}
	client := api.NewClient(cfg.ServerURL, cfg.NodeToken)
	response, err := client.VerifySync(context.Background(), api.VerifySyncRequest{
		NodeID: cfg.NodeID, DeviceID: deviceID, SSHKeyID: sshKeyID,
	})
	if err != nil {
		return err
	}
	if response.Data.SSHKeyID != sshKeyID {
		return fmt.Errorf("server did not verify the exact SSH key for synchronization")
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(originalCommand))
	args := []string{
		"-n", cfg.RuntimeBinaryPath, "sync-command",
		"--user", response.Data.UserID,
		"--command-base64", encoded,
		"--workspace-root", cfg.WorkspaceRoot,
	}
	if dryRun {
		fmt.Printf("sudo -n %s sync-command --user %s --command-base64 <redacted>\n", cfg.RuntimeBinaryPath, response.Data.UserID)
		return nil
	}
	cmd := exec.Command("sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func sessionFromOriginalCommand(command string) (string, error) {
	kind, id, err := attachTargetFromOriginalCommand(command)
	if err != nil || kind != "session" {
		return "", fmt.Errorf("SSH command is not an allowed agent-remote session attach request")
	}
	return id, nil
}

func attachTargetFromOriginalCommand(command string) (string, string, error) {
	target, err := attachRequestFromOriginalCommand(command)
	return target.kind, target.id, err
}

type attachTarget struct {
	kind           string
	id             string
	bindingSession string
	expectedDevice string
}

func attachRequestFromOriginalCommand(command string) (attachTarget, error) {
	if len(command) > 512 || strings.ContainsAny(command, "\x00\r\n") {
		return attachTarget{}, fmt.Errorf("SSH attach command is invalid")
	}
	fields := strings.Fields(command)
	if (len(fields) != 3 && len(fields) != 5) || fields[0] != "agent-remote-attach" || (fields[1] != "--session" && fields[1] != "--binding") {
		return attachTarget{}, fmt.Errorf("SSH command is not an allowed agent-remote attach request")
	}
	if err := validateCommandID(fields[2], "attach target"); err != nil {
		return attachTarget{}, err
	}
	target := attachTarget{kind: strings.TrimPrefix(fields[1], "--"), id: fields[2]}
	if len(fields) == 5 {
		if target.kind == "session" && fields[3] == "--expected-device" {
			if err := validateCommandID(fields[4], "expected device ID"); err != nil {
				return attachTarget{}, err
			}
			target.expectedDevice = fields[4]
			return target, nil
		}
		if target.kind != "binding" || fields[3] != "--binding-session" {
			return attachTarget{}, fmt.Errorf("SSH binding command is invalid")
		}
		if err := validateCommandID(fields[4], "binding session ID"); err != nil {
			return attachTarget{}, err
		}
		target.bindingSession = fields[4]
	}
	return target, nil
}

func tunnelFromOriginalCommand(command string) (string, error) {
	fields := strings.Fields(command)
	if len(fields) != 5 || fields[0] != "agent-remote-tunnel" || fields[1] != "--forward" || fields[3] != "--protocol" || fields[4] != "1" {
		return "", fmt.Errorf("SSH command is not an allowed agent-remote tunnel request")
	}
	if err := validateCommandID(fields[2], "forward ID"); err != nil {
		return "", err
	}
	return fields[2], nil
}

func validateCommandID(value string, name string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("%s is invalid", name)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == ':' {
			continue
		}
		return fmt.Errorf("%s contains unsafe characters", name)
	}
	return nil
}

type stdioConnection struct {
	reader io.ReadCloser
	writer io.WriteCloser
	once   sync.Once
}

func (c *stdioConnection) Read(payload []byte) (int, error) {
	return c.reader.Read(payload)
}

func (c *stdioConnection) Write(payload []byte) (int, error) {
	return c.writer.Write(payload)
}

func (c *stdioConnection) Close() error {
	var closeErr error
	c.once.Do(func() {
		if err := c.reader.Close(); err != nil {
			closeErr = err
		}
		if err := c.writer.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func (c *stdioConnection) LocalAddr() net.Addr  { return stdioAddress("local-stdio") }
func (c *stdioConnection) RemoteAddr() net.Addr { return stdioAddress("remote-stdio") }
func (c *stdioConnection) SetDeadline(deadline time.Time) error {
	if reader, ok := c.reader.(interface{ SetDeadline(time.Time) error }); ok {
		_ = reader.SetDeadline(deadline)
	}
	if writer, ok := c.writer.(interface{ SetDeadline(time.Time) error }); ok {
		_ = writer.SetDeadline(deadline)
	}
	return nil
}
func (c *stdioConnection) SetReadDeadline(deadline time.Time) error {
	if reader, ok := c.reader.(interface{ SetReadDeadline(time.Time) error }); ok {
		return reader.SetReadDeadline(deadline)
	}
	return nil
}
func (c *stdioConnection) SetWriteDeadline(deadline time.Time) error {
	if writer, ok := c.writer.(interface{ SetWriteDeadline(time.Time) error }); ok {
		return writer.SetWriteDeadline(deadline)
	}
	return nil
}

type stdioAddress string

func (a stdioAddress) Network() string { return "stdio" }
func (a stdioAddress) String() string  { return string(a) }
