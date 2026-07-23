package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	force := fs.Bool("force", false, "replace an existing node registration")
	version := fs.String("version", config.DefaultVersion, "node version")
	runtimeBackends := fs.String("runtime-backends", "", "comma-separated runtime backends")
	systemInstall := fs.Bool("system-install", false, "use system service paths")
	prefix := fs.String("prefix", "/usr/local", "system installation prefix")
	stateDir := fs.String("state-dir", "/var/lib/agent-remote-node", "system service state directory")
	dataDir := fs.String("data-dir", "/var/lib/agent-remote", "managed workspace and account data directory")
	claudeRuntimePath := fs.String(
		"claude-runtime-path",
		"/opt/agent-remote/runtimes/claude/current/bin/claude",
		"managed Claude executable",
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *registrationToken == "" {
		return fmt.Errorf("registration-token is required")
	}
	if !*force {
		existing, err := config.Load(*configPath)
		if err == nil && existing.NodeToken != "" && existing.NodeToken != "node_replace_me" &&
			existing.NodeID == *nodeID && strings.TrimRight(existing.ServerURL, "/") == strings.TrimRight(*serverURL, "/") {
			existing.Version = *version
			if *runtimeBackends != "" {
				existing.AllowedRuntimeBackends = splitCommaList(*runtimeBackends)
			}
			if *systemInstall {
				applySystemInstallPaths(&existing, *prefix, *stateDir, *dataDir, *claudeRuntimePath)
			}
			if err := existing.Validate(true); err != nil {
				return err
			}
			if err := config.Save(*configPath, existing); err != nil {
				return err
			}
			fmt.Printf("node %s is already registered; refreshed local configuration\n", existing.NodeID)
			return nil
		}
	}
	cfg := config.Config{
		ServerURL:          *serverURL,
		NodeID:             *nodeID,
		Version:            *version,
		SupportedToolTypes: []string{"claude"},
	}.WithDefaults()
	if *runtimeBackends != "" {
		cfg.AllowedRuntimeBackends = splitCommaList(*runtimeBackends)
	}
	if *systemInstall {
		applySystemInstallPaths(&cfg, *prefix, *stateDir, *dataDir, *claudeRuntimePath)
	}
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

func splitCommaList(value string) []string {
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func applySystemInstallPaths(cfg *config.Config, prefix string, stateDir string, dataDir string, claudeRuntimePath string) {
	cfg.LedgerPath = filepath.Join(stateDir, "ledger.json")
	cfg.SSHAuthorizedKeysPath = filepath.Join(stateDir, "authorized_keys")
	cfg.AttachBinaryPath = filepath.Join(prefix, "bin", "agent-remote-attach")
	cfg.WorkspaceRoot = filepath.Join(dataDir, "users")
	cfg.AccountRoot = cfg.WorkspaceRoot
	cfg.BrowserRoot = filepath.Join(dataDir, "browser-sessions")
	cfg.RuntimeBinaryPath = filepath.Join(prefix, "bin", "agent-remote-runtime")
	cfg.ClaudeRuntimePath = claudeRuntimePath
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
