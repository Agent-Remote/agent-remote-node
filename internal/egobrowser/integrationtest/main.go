// Command integrationtest exposes the production broker to the cross-repository
// relay E2E. It is deliberately kept below internal/ and is not a release binary.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowser"
)

type runtimeContext struct {
	BrokerSocket string `json:"broker_socket"`
	StartupNonce string `json:"startup_nonce"`
}

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("ego-browser-broker-integration", flag.ContinueOnError)
	serverURL := flags.String("server-url", "", "control-plane origin")
	nodeID := flags.String("node-id", "", "node UUID")
	nodeToken := flags.String("node-token", "", "node bearer token")
	toolSessionID := flags.String("tool-session-id", "", "tool-session UUID")
	socketPath := flags.String("socket", "", "broker Unix socket")
	stateRoot := flags.String("state-root", "", "broker state directory")
	contextFile := flags.String("context-file", "", "owner-only wrapper context output")
	caCertificate := flags.String("ca-certificate", "", "PEM trust root for the test origin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"server-url":      *serverURL,
		"node-id":         *nodeID,
		"node-token":      *nodeToken,
		"tool-session-id": *toolSessionID,
		"socket":          *socketPath,
		"state-root":      *stateRoot,
		"context-file":    *contextFile,
		"ca-certificate":  *caCertificate,
	} {
		if value == "" {
			return fmt.Errorf("%s is required", name)
		}
	}

	rootPEM, err := os.ReadFile(*caCertificate)
	if err != nil {
		return err
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(rootPEM) {
		return errors.New("ca-certificate contains no trusted PEM certificate")
	}
	client := api.NewClientWithTLSConfig(*serverURL, *nodeToken, &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    rootCAs,
	})
	broker, err := egobrowser.New(egobrowser.Config{
		Enabled:                      true,
		NodeID:                       *nodeID,
		SocketPath:                   *socketPath,
		StateRoot:                    *stateRoot,
		Client:                       client,
		ControlPlaneConfigured:       true,
		LeaseSeconds:                 60,
		RenewIntervalSeconds:         1,
		RenewGraceSeconds:            10,
		AdmissionMinRemainingSeconds: 20,
		MaxParallelRequests:          4,
		MaxScriptBytes:               1 << 20,
		MaxExecuteTimeoutMS:          120_000,
		WrapperVersion:               "0.1.0",
	})
	if err != nil {
		return err
	}
	defer broker.Close()

	nonce, _, err := broker.RegisterToolSession(*toolSessionID)
	if err != nil {
		return err
	}
	if err := broker.AuthorizeToolSessionPeer(*toolSessionID, nonce, uint32(os.Getuid())); err != nil {
		return err
	}
	if err := broker.Refresh(context.Background()); err != nil {
		return fmt.Errorf("initial browser binding refresh: %w", err)
	}
	if err := writeRuntimeContext(*contextFile, runtimeContext{
		BrokerSocket: *socketPath,
		StartupNonce: nonce,
	}); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return broker.Run(ctx)
}

func writeRuntimeContext(path string, value runtimeContext) error {
	if !filepath.IsAbs(path) {
		return errors.New("context-file must be absolute")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
