package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
	"github.com/Agent-Remote/agent-remote-node/internal/skillexport"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func frozenExportFromCommand(command string) (string, error) {
	parts := strings.Fields(command)
	if len(parts) != 5 || parts[0] != "agent-remote-skill-export" || parts[1] != "--snapshot" || parts[3] != "--protocol" || parts[4] != "1" ||
		parts[2] == "" || skillmanager.ValidateFinalizationCursor(parts[2]) != nil {
		return "", skillexport.ErrUnavailable
	}
	return parts[2], nil
}

func runFrozenExport(cfg config.Config, deviceID, keyID, snapshotID string, dryRun bool) error {
	if deviceID == "" || keyID == "" || skillmanager.ValidateFinalizationCursor(deviceID) != nil || skillmanager.ValidateFinalizationCursor(keyID) != nil {
		return skillexport.ErrUnavailable
	}
	if dryRun {
		fmt.Printf("agent-remote-skill-export --snapshot %s --protocol 1\n", snapshotID)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return skillexport.Serve(ctx, &stdioConnection{reader: os.Stdin, writer: os.Stdout}, skillexport.Identity{
		NodeID: cfg.NodeID, SnapshotID: snapshotID, DeviceID: deviceID, SSHKeyID: keyID,
	}, api.NewClient(cfg.ServerURL, cfg.NodeToken), runtimehelper.NewClient(cfg.RuntimeSocketPath))
}
