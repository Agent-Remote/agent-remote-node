package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/runtimehelper"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agent-remote-runtime <serve|probe|supervise|exec|attach|sync-command>")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "probe":
		return probe(args[1:])
	case "supervise":
		return executeSpec(args[1:], runtimehelper.SuperviseSpec)
	case "exec":
		return executeSpec(args[1:], runtimehelper.ExecSpec)
	case "attach":
		return attach(args[1:])
	case "sync-command":
		return syncCommand(args[1:])
	default:
		return fmt.Errorf("unknown runtime command %q", args[0])
	}
}

func syncCommand(args []string) error {
	fs := flag.NewFlagSet("sync-command", flag.ContinueOnError)
	userID := fs.String("user", "", "control-plane user ID")
	encodedCommand := fs.String("command-base64", "", "URL-safe base64 SSH command")
	workspaceRoot := fs.String("workspace-root", "/var/lib/agent-remote/users", "workspace root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *userID == "" || *encodedCommand == "" {
		return errors.New("user and command-base64 are required")
	}
	command, err := base64.RawURLEncoding.DecodeString(*encodedCommand)
	if err != nil {
		return errors.New("command-base64 is invalid")
	}
	return runtimehelper.SyncCommand(
		runtimehelper.EngineConfig{WorkspaceRoot: *workspaceRoot, AccountRoot: *workspaceRoot},
		*userID,
		string(command),
	)
}

func attach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	sessionID := fs.String("session", "", "session ID")
	stateRoot := fs.String("state-root", "/var/lib/agent-remote-runtime", "runtime state root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" {
		return errors.New("session is required")
	}
	return runtimehelper.AttachSession(runtimehelper.EngineConfig{StateRoot: *stateRoot}, *sessionID)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	socketPath := fs.String("socket", "/run/agent-remote/runtime.sock", "Unix socket path")
	stateRoot := fs.String("state-root", "/var/lib/agent-remote-runtime", "runtime state root")
	workspaceRoot := fs.String("workspace-root", "/var/lib/agent-remote/users", "workspace root")
	accountRoot := fs.String("account-root", "/var/lib/agent-remote/users", "account root")
	claudeRuntime := fs.String("claude-runtime", "/opt/agent-remote/runtimes/claude/current/bin/claude", "managed Claude executable")
	groupName := fs.String("group", "agent-remote", "socket access group")
	userName := fs.String("user", "agent-remote", "authorized node worker user")
	wireGuardInterface := fs.String("wireguard-interface", "agent-remote", "managed WireGuard interface")
	wireGuardPrivateKey := fs.String("wireguard-private-key", "/etc/agent-remote-node/wireguard.key", "root-owned WireGuard private key")
	wireGuardListenPort := fs.Int("wireguard-listen-port", 51820, "WireGuard UDP listen port")
	wgBinary := fs.String("wg-binary", "wg", "WireGuard control binary")
	nodeConfigPath := fs.String("node-config", "/etc/agent-remote-node/config.json", "node configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	groupID, err := lookupGroupID(*groupName)
	if err != nil {
		return err
	}
	userID, err := lookupUserID(*userName)
	if err != nil {
		return err
	}
	config := runtimehelper.EngineConfig{
		StateRoot:           *stateRoot,
		WorkspaceRoot:       *workspaceRoot,
		AccountRoot:         *accountRoot,
		RuntimeBinaryPath:   os.Args[0],
		ClaudeRuntimePath:   *claudeRuntime,
		DataGroup:           *groupName,
		NodeUser:            *userName,
		WireGuardInterface:  *wireGuardInterface,
		WireGuardPrivateKey: *wireGuardPrivateKey,
		WireGuardListenPort: *wireGuardListenPort,
		WGBinaryPath:        *wgBinary,
	}
	if err := applyBrowserConfig(&config, *nodeConfigPath); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := runtimehelper.NewServer(*socketPath, groupID, userID, runtimehelper.NewEngine(config))
	return server.Serve(ctx)
}

func applyBrowserConfig(runtimeConfig *runtimehelper.EngineConfig, path string) error {
	nodeConfig, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load node config: %w", err)
	}
	runtimeConfig.DockerBinaryPath = nodeConfig.DockerBinaryPath
	runtimeConfig.BrowserRoot = nodeConfig.BrowserRoot
	runtimeConfig.BrowserImage = nodeConfig.BrowserImage
	runtimeConfig.BrowserPublicBaseURL = nodeConfig.BrowserPublicBaseURL
	runtimeConfig.BrowserDockerNetwork = nodeConfig.BrowserDockerNetwork
	return nil
}

func probe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	socketPath := fs.String("socket", "/run/agent-remote/runtime.sock", "Unix socket path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := runtimehelper.NewClient(*socketPath).Call(context.Background(), "probe", "probe", map[string]any{})
	if err != nil {
		return err
	}
	data, err := jsonMarshal(result)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func executeSpec(args []string, action func(runtimehelper.EngineConfig, string) error) error {
	fs := flag.NewFlagSet("spec", flag.ContinueOnError)
	specPath := fs.String("spec", "", "validated session spec")
	stateRoot := fs.String("state-root", "/var/lib/agent-remote-runtime", "runtime state root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *specPath == "" {
		return errors.New("spec is required")
	}
	return action(runtimehelper.EngineConfig{StateRoot: *stateRoot, RuntimeBinaryPath: os.Args[0]}, *specPath)
}

func lookupGroupID(name string) (int, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return -1, err
	}
	return strconv.Atoi(group.Gid)
}

func lookupUserID(name string) (int, error) {
	found, err := user.Lookup(name)
	if err != nil {
		return -1, err
	}
	return strconv.Atoi(found.Uid)
}

func jsonMarshal(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}
