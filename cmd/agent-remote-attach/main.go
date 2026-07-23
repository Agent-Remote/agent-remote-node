package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("agent-remote-attach", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath(), "config path")
	sessionID := fs.String("session", "", "session ID")
	bindingID := fs.String("binding", "", "tool account ID for a binding session")
	deviceID := fs.String("device", "", "device ID")
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
	targetKind := "session"
	targetID := *sessionID
	if *bindingID != "" {
		targetKind, targetID = "binding", *bindingID
	}
	if targetID == "" {
		originalCommand := os.Getenv("SSH_ORIGINAL_COMMAND")
		if kind, id, parseErr := attachTargetFromOriginalCommand(originalCommand); parseErr == nil {
			targetKind, targetID = kind, id
		} else {
			return runSyncGateway(cfg, *deviceID, originalCommand, *dryRun)
		}
	}
	client := api.NewClient(cfg.ServerURL, cfg.NodeToken)
	var runtimeBackend, runtimeSessionID, tmuxSessionName string
	if targetKind == "binding" {
		response, err := client.VerifyBindingAttach(context.Background(), api.VerifyBindingAttachRequest{
			NodeID: cfg.NodeID, ToolAccountID: targetID, DeviceID: *deviceID,
		})
		if err != nil {
			return err
		}
		runtimeBackend = response.Data.RuntimeBackend
		runtimeSessionID = response.Data.BindingSessionID
		tmuxSessionName = response.Data.TmuxSessionName
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
	}
	argsForTmux := []string{"attach-session", "-t", tmuxSessionName}
	command := "tmux"
	if runtimeBackend == "native" {
		command = "sudo"
		argsForTmux = []string{"-n", cfg.RuntimeBinaryPath, "attach", "--session", runtimeSessionID}
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

func runSyncGateway(cfg config.Config, deviceID string, originalCommand string, dryRun bool) error {
	if originalCommand == "" || len(originalCommand) > 4096 || strings.ContainsAny(originalCommand, "\x00\r\n") {
		return fmt.Errorf("SSH sync command is invalid")
	}
	client := api.NewClient(cfg.ServerURL, cfg.NodeToken)
	response, err := client.VerifySync(context.Background(), api.VerifySyncRequest{
		NodeID: cfg.NodeID, DeviceID: deviceID,
	})
	if err != nil {
		return err
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
	fields := strings.Fields(command)
	if len(fields) != 3 || fields[0] != "agent-remote-attach" || (fields[1] != "--session" && fields[1] != "--binding") {
		return "", "", fmt.Errorf("SSH command is not an allowed agent-remote attach request")
	}
	for _, character := range fields[2] {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == ':' {
			continue
		}
		return "", "", fmt.Errorf("attach target contains unsafe characters")
	}
	if fields[2] == "" || len(fields[2]) > 128 {
		return "", "", fmt.Errorf("attach target is invalid")
	}
	return strings.TrimPrefix(fields[1], "--"), fields[2], nil
}
