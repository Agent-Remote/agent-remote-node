package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
)

func verifyExactSession(cfg config.Config, client api.Client, sessionID, expectedDevice, deviceID, sshKeyID string) (api.VerifyExactAttachResponse, error) {
	if expectedDevice != deviceID {
		return api.VerifyExactAttachResponse{}, errors.New("SSH device differs from the reviewed session device")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return client.VerifyExactAttach(ctx, api.VerifyExactAttachRequest{
		NodeID: cfg.NodeID, SessionID: sessionID, DeviceID: deviceID, SSHKeyID: sshKeyID,
	})
}
