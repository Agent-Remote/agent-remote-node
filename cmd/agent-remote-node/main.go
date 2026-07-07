package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/sshkeys"
	"github.com/Agent-Remote/agent-remote-node/internal/worker"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printUsage()
		return nil
	}
	switch args[0] {
	case "register":
		return register(args[1:])
	case "heartbeat":
		return withWorker(args[1:], func(ctx context.Context, w worker.Worker) error {
			return w.Heartbeat(ctx)
		})
	case "poll-once":
		return withWorker(args[1:], func(ctx context.Context, w worker.Worker) error {
			return w.PollOnce(ctx)
		})
	case "reconcile":
		return withWorker(args[1:], func(ctx context.Context, w worker.Worker) error {
			return w.Reconcile(ctx)
		})
	case "run":
		return withWorker(args[1:], func(ctx context.Context, w worker.Worker) error {
			return w.Run(ctx)
		})
	case "install-ssh":
		return installSSH(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func register(args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config path")
	serverURL := fs.String("server-url", "", "server URL")
	nodeID := fs.String("node-id", "", "node ID")
	registrationToken := fs.String("registration-token", "", "registration token")
	version := fs.String("version", config.DefaultVersion, "node version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *registrationToken == "" {
		return fmt.Errorf("registration-token is required")
	}
	cfg := config.Config{
		ServerURL:          *serverURL,
		NodeID:             *nodeID,
		Version:            *version,
		SupportedToolTypes: []string{"claude"},
	}.WithDefaults()
	if err := cfg.Validate(false); err != nil {
		return err
	}
	client := api.NewClient(cfg.ServerURL, "")
	response, err := client.RegisterNode(context.Background(), api.RegisterNodeRequest{
		NodeID:            cfg.NodeID,
		RegistrationToken: *registrationToken,
		Version:           cfg.Version,
	})
	if err != nil {
		return err
	}
	cfg.NodeToken = response.Data.NodeToken
	if err := config.Save(*configPath, cfg); err != nil {
		return err
	}
	fmt.Printf("registered node %s\n", response.Data.NodeID)
	return nil
}

func installSSH(args []string) error {
	fs := flag.NewFlagSet("install-ssh", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := sshkeys.Sync(cfg.SSHAuthorizedKeysPath, cfg.AttachBinaryPath, sshkeys.SyncPayload{}); err != nil {
		return err
	}
	fmt.Printf("prepared managed authorized_keys at %s\n", cfg.SSHAuthorizedKeysPath)
	return nil
}

func withWorker(args []string, fn func(context.Context, worker.Worker) error) error {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(true); err != nil {
		return err
	}
	taskLedger, err := ledger.Open(cfg.LedgerPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w := worker.New(cfg, api.NewClient(cfg.ServerURL, cfg.NodeToken), taskLedger)
	return fn(ctx, w)
}

func printUsage() {
	fmt.Println("agent-remote-node <register|heartbeat|poll-once|reconcile|run|install-ssh> [flags]")
}
