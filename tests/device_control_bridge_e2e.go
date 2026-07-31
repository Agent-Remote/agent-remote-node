package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/devicecontrol"
)

const maximumConfigBytes = 16 * 1024

type configuration struct {
	ServerURL    string         `json:"server_url"`
	NodeToken    string         `json:"node_token"`
	NodeID       string         `json:"node_id"`
	BridgeSocket string         `json:"bridge_socket"`
	Activation   map[string]any `json:"activation"`
}

type diagnosticClient struct {
	client api.Client
}

func (client diagnosticClient) RegisterDeviceRelayMaterial(ctx context.Context, sessionID string, request api.DeviceRelayMaterialRequest) (api.DeviceRelayMaterialResponse, error) {
	response, err := client.client.RegisterDeviceRelayMaterial(ctx, sessionID, request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register device relay material failed")
	}
	return response, err
}

func (client diagnosticClient) OpenDeviceRelay(ctx context.Context, sessionID string, relayPath string, relayTicket string) (io.ReadWriteCloser, error) {
	connection, err := client.client.OpenDeviceRelay(ctx, sessionID, relayPath, relayTicket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open device relay failed")
	}
	return connection, err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := os.Getenv("AGENT_REMOTE_DEVICE_E2E_CONFIG")
	if configPath == "" || !filepath.IsAbs(configPath) {
		return errors.New("AGENT_REMOTE_DEVICE_E2E_CONFIG must be an absolute path")
	}
	config, err := loadConfiguration(configPath)
	if err != nil {
		return err
	}
	activation, err := devicecontrol.DecodeActivatePayload(config.Activation, config.NodeID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("decode activation: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := diagnosticClient{client: api.NewClient(config.ServerURL, config.NodeToken)}
	manager := devicecontrol.NewBridgeManager(client)
	if err := manager.Start(ctx, activation, config.BridgeSocket); err != nil {
		return fmt.Errorf("start bridge: %w", err)
	}
	defer manager.StopAll()
	fmt.Println("device-control bridge ready")
	<-ctx.Done()
	return nil
}

func loadConfiguration(path string) (configuration, error) {
	var config configuration
	info, err := os.Lstat(path)
	if err != nil {
		return config, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || info.Size() > maximumConfigBytes {
		return config, errors.New("E2E configuration file is unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return config, errors.New("E2E configuration must contain one JSON object")
	}
	if config.ServerURL == "" || config.NodeToken == "" || config.BridgeSocket == "" {
		return config, errors.New("E2E configuration is incomplete")
	}
	return config, nil
}
