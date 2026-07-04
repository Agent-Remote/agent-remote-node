package main

import (
	"context"
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
	configPath := fs.String("config", "config.json", "config path")
	sessionID := fs.String("session", "", "session ID")
	deviceID := fs.String("device", "", "device ID")
	dryRun := fs.Bool("dry-run", false, "print tmux command without executing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return fmt.Errorf("session is required")
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
	client := api.NewClient(cfg.ServerURL, cfg.NodeToken)
	response, err := client.VerifyAttach(context.Background(), api.VerifyAttachRequest{
		NodeID:    cfg.NodeID,
		SessionID: *sessionID,
		DeviceID:  *deviceID,
	})
	if err != nil {
		return err
	}
	argsForTmux := []string{"attach-session", "-t", response.Data.TmuxSessionName}
	if *dryRun {
		fmt.Printf("tmux %s\n", strings.Join(argsForTmux, " "))
		return nil
	}
	cmd := exec.Command("tmux", argsForTmux...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
