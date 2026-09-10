package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
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
	case "configure-wireguard":
		return configureWireGuard(args[1:])
	case "configure-ego-browser":
		return configureEgoBrowser(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func configureWireGuard(args []string) error {
	fs := flag.NewFlagSet("configure-wireguard", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config path")
	publicKey := fs.String("public-key", "", "WireGuard public key")
	address := fs.String("address", "10.77.0.1/24", "WireGuard interface address")
	endpoint := fs.String("endpoint", "", "public WireGuard endpoint")
	interfaceName := fs.String("interface", "agent-remote", "WireGuard interface")
	privateKeyPath := fs.String("private-key-path", "/etc/agent-remote-node/wireguard.key", "root-owned private key path")
	listenPort := fs.Int("listen-port", 51820, "WireGuard UDP listen port")
	version := fs.String("version", "", "node version to persist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *publicKey == "" {
		return errors.New("public-key is required")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	resolvedEndpoint := *endpoint
	if resolvedEndpoint == "" {
		serverURL, err := url.Parse(cfg.ServerURL)
		if err != nil || serverURL.Hostname() == "" {
			return errors.New("cannot infer WireGuard endpoint from server_url")
		}
		resolvedEndpoint = net.JoinHostPort(serverURL.Hostname(), fmt.Sprintf("%d", *listenPort))
	}
	cfg.WireGuardInterface = *interfaceName
	cfg.WireGuardPrivateKeyPath = *privateKeyPath
	cfg.WireGuardAddress = *address
	cfg.WireGuardPublicKey = *publicKey
	cfg.WireGuardEndpoint = resolvedEndpoint
	cfg.WireGuardListenPort = *listenPort
	if *version != "" {
		cfg.Version = *version
	}
	if err := config.Save(*configPath, cfg); err != nil {
		return err
	}
	fmt.Printf("configured WireGuard endpoint for node %s\n", cfg.NodeID)
	return nil
}

type egoBrowserRuntimeMetadata struct {
	WrapperPath     string
	WrapperVersion  string
	SkillPath       string
	SkillVersion    string
	SkillTreeSHA256 string
}

// configureEgoBrowser synchronizes the config with the verified installed
// wrapper while preserving the operator's explicit enabled state.
func configureEgoBrowser(args []string) error {
	fs := flag.NewFlagSet("configure-ego-browser", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config path")
	runtimeRoot := fs.String("runtime-root", "/opt/agent-remote/ego-browser", "managed ego-browser runtime root")
	enable := fs.Bool("enable", false, "enable the ego-browser bridge after verification")
	disable := fs.Bool("disable", false, "disable the ego-browser bridge")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *enable && *disable {
		return errors.New("--enable and --disable are mutually exclusive")
	}
	// Upgrades must be able to read a previously enabled config whose wrapper
	// pin predates the reviewed release. The installed runtime is verified
	// below before the ordinary (strict) config save is attempted.
	cfg, err := config.LoadForUpgrade(*configPath)
	if err != nil {
		return err
	}
	metadata, err := readEgoBrowserRuntimeMetadata(*runtimeRoot)
	if err != nil {
		return err
	}
	if metadata.WrapperVersion != egobrowserartifact.PinnedWrapperVersion {
		return fmt.Errorf("installed ego-browser wrapper version %q is not the reviewed Node pin %q",
			metadata.WrapperVersion, egobrowserartifact.PinnedWrapperVersion)
	}
	cfg.EgoBrowserWrapperPath = metadata.WrapperPath
	cfg.EgoBrowserWrapperVersion = metadata.WrapperVersion
	cfg.EgoBrowserSkillPath = metadata.SkillPath
	cfg.EgoBrowserSkillVersion = metadata.SkillVersion
	cfg.EgoBrowserSkillTreeSHA256 = metadata.SkillTreeSHA256
	cfg.EgoBrowserProtocolVersion = "ego-browser-bridge-v1"
	if *enable {
		cfg.EgoBrowserEnabled = true
	}
	if *disable {
		cfg.EgoBrowserEnabled = false
	}
	if cfg.EgoBrowserEnabled {
		if err := egobrowserartifact.Verify(egobrowserartifact.RuntimeConfig{
			WrapperPath: metadata.WrapperPath, WrapperVersion: metadata.WrapperVersion,
			SkillPath: metadata.SkillPath, SkillVersion: metadata.SkillVersion,
			SkillTreeSHA256: metadata.SkillTreeSHA256,
		}); err != nil {
			return fmt.Errorf("ego-browser runtime verification failed: %w", err)
		}
	}
	if err := cfg.Validate(false); err != nil {
		return err
	}
	if err := config.Save(*configPath, cfg); err != nil {
		return err
	}
	fmt.Printf("ego-browser configuration synchronized: wrapper=%s enabled=%t\n", cfg.EgoBrowserWrapperVersion, cfg.EgoBrowserEnabled)
	return nil
}

func readEgoBrowserRuntimeMetadata(runtimeRoot string) (egoBrowserRuntimeMetadata, error) {
	if !validAbsoluteManagedPath(runtimeRoot) {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser runtime root is invalid")
	}
	if err := validateManagedEntry(runtimeRoot, true, false); err != nil {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser runtime root is invalid")
	}
	releasesRoot := filepath.Join(runtimeRoot, "releases")
	if err := validateManagedEntry(releasesRoot, true, false); err != nil {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser releases directory is invalid")
	}
	current := filepath.Join(runtimeRoot, "current")
	currentInfo, err := os.Lstat(current)
	if err != nil || currentInfo.Mode()&os.ModeSymlink == 0 {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser current release link is invalid")
	}
	releaseRoot, err := filepath.EvalSymlinks(current)
	if err != nil {
		return egoBrowserRuntimeMetadata{}, fmt.Errorf("resolve ego-browser runtime: %w", err)
	}
	resolvedReleasesRoot, err := filepath.EvalSymlinks(releasesRoot)
	if err != nil {
		return egoBrowserRuntimeMetadata{}, fmt.Errorf("resolve ego-browser releases: %w", err)
	}
	relative, err := filepath.Rel(resolvedReleasesRoot, releaseRoot)
	if err != nil || relative == "." || relative == ".." || strings.Contains(relative, string(filepath.Separator)) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser runtime points outside managed releases")
	}
	if err := validateManagedEntry(releaseRoot, true, false); err != nil {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser release directory is invalid")
	}
	metadata := egoBrowserRuntimeMetadata{
		WrapperPath: filepath.Join(current, "bin", "ego-browser"),
		SkillPath:   filepath.Join(current, "skill", "ego-browser"),
	}
	for name, destination := range map[string]*string{
		"VERSION":           &metadata.WrapperVersion,
		"SKILL_VERSION":     &metadata.SkillVersion,
		"SKILL_TREE_SHA256": &metadata.SkillTreeSHA256,
	} {
		value, err := readEgoBrowserMetadataLine(filepath.Join(current, name))
		if err != nil {
			return egoBrowserRuntimeMetadata{}, err
		}
		*destination = value
	}
	resolvedWrapper, err := filepath.EvalSymlinks(metadata.WrapperPath)
	if err != nil || !pathWithin(resolvedReleasesRoot, resolvedWrapper) {
		return egoBrowserRuntimeMetadata{}, errors.New("installed ego-browser wrapper is unavailable")
	}
	if err := validateManagedEntry(metadata.WrapperPath, false, true); err != nil {
		return egoBrowserRuntimeMetadata{}, errors.New("installed ego-browser wrapper is unavailable")
	}
	resolvedSkill, err := filepath.EvalSymlinks(metadata.SkillPath)
	if err != nil || !pathWithin(resolvedReleasesRoot, resolvedSkill) {
		return egoBrowserRuntimeMetadata{}, errors.New("installed ego-browser Skill is unavailable")
	}
	if err := validateManagedEntry(metadata.SkillPath, true, false); err != nil {
		return egoBrowserRuntimeMetadata{}, errors.New("installed ego-browser Skill is unavailable")
	}
	if err := validateSkillTree(metadata.SkillPath); err != nil {
		return egoBrowserRuntimeMetadata{}, errors.New("installed ego-browser Skill is incomplete")
	}
	if !validEgoBrowserMetadataValue(metadata.WrapperVersion) || !validEgoBrowserMetadataValue(metadata.SkillVersion) ||
		!validEgoBrowserDigest(metadata.SkillTreeSHA256) {
		return egoBrowserRuntimeMetadata{}, errors.New("installed ego-browser metadata is invalid")
	}
	if filepath.Base(releaseRoot) != metadata.WrapperVersion {
		return egoBrowserRuntimeMetadata{}, errors.New("ego-browser release version metadata does not match its directory")
	}
	return metadata, nil
}

func readEgoBrowserMetadataLine(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("ego-browser metadata is invalid")
	}
	if info.Size() <= 0 || info.Size() > 512 {
		return "", errors.New("ego-browser metadata is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read ego-browser metadata: %w", err)
	}
	if len(data) == 0 || len(data) > 512 {
		return "", errors.New("ego-browser metadata is invalid")
	}
	value := strings.TrimSpace(string(data))
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("ego-browser metadata is invalid")
	}
	return value, nil
}

func validAbsoluteManagedPath(value string) bool {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	for _, component := range strings.Split(value, string(filepath.Separator)) {
		if component == ".." {
			return false
		}
	}
	return true
}

func pathWithin(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	return candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator))
}

func validateManagedEntry(path string, directory bool, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&0o022 != 0 {
		return errors.New("managed entry is unsafe")
	}
	if directory {
		if !info.IsDir() {
			return errors.New("managed entry is not a directory")
		}
	} else if !info.Mode().IsRegular() {
		return errors.New("managed entry is not a regular file")
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return errors.New("managed entry is not executable")
	}
	return nil
}

func validateSkillTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("Skill tree contains a symlink")
		}
		if entry.IsDir() {
			return validateManagedEntry(path, true, false)
		}
		return validateManagedEntry(path, false, false)
	})
}

func validEgoBrowserMetadataValue(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || (index > 0 && strings.ContainsRune("._+-", character)) {
			continue
		}
		return false
	}
	return true
}

func validEgoBrowserDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
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
	deviceProxyPath := fs.String(
		"device-proxy-path",
		"/opt/agent-remote/device/current/bin/agent-remote-device-proxy",
		"managed device proxy executable",
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *registrationToken == "" {
		return fmt.Errorf("registration-token is required")
	}
	if !*force {
		existing, err := config.LoadForUpgrade(*configPath)
		if err == nil && existing.NodeToken != "" && existing.NodeToken != "node_replace_me" &&
			existing.NodeID == *nodeID && strings.TrimRight(existing.ServerURL, "/") == strings.TrimRight(*serverURL, "/") {
			existing.Version = *version
			if *runtimeBackends != "" {
				existing.AllowedRuntimeBackends = splitCommaList(*runtimeBackends)
			}
			if *systemInstall {
				applySystemInstallPaths(&existing, *prefix, *stateDir, *dataDir, *claudeRuntimePath, *deviceProxyPath)
			}
			if err := existing.ValidateForUpgrade(true); err != nil {
				return err
			}
			if err := config.SaveForUpgrade(*configPath, existing); err != nil {
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
		applySystemInstallPaths(&cfg, *prefix, *stateDir, *dataDir, *claudeRuntimePath, *deviceProxyPath)
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

func applySystemInstallPaths(cfg *config.Config, prefix string, stateDir string, dataDir string, claudeRuntimePath string, deviceProxyPath string) {
	cfg.LedgerPath = filepath.Join(stateDir, "ledger.json")
	cfg.SSHAuthorizedKeysPath = filepath.Join(stateDir, "authorized_keys")
	cfg.AttachBinaryPath = filepath.Join(prefix, "bin", "agent-remote-attach")
	cfg.WorkspaceRoot = filepath.Join(dataDir, "users")
	cfg.AccountRoot = cfg.WorkspaceRoot
	cfg.BrowserRoot = filepath.Join(dataDir, "browser-sessions")
	cfg.RuntimeBinaryPath = filepath.Join(prefix, "bin", "agent-remote-runtime")
	cfg.ClaudeRuntimePath = claudeRuntimePath
	cfg.DeviceProxyPath = deviceProxyPath
	cfg.DeviceControlRoot = filepath.Join(dataDir, "device-sessions")
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
	if _, err := os.Stat(cfg.SSHAuthorizedKeysPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := sshkeys.Sync(cfg.SSHAuthorizedKeysPath, cfg.AttachBinaryPath, cfg.SourcePath, sshkeys.SyncPayload{}); err != nil {
			return err
		}
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
	fmt.Println("agent-remote-node <register|heartbeat|poll-once|reconcile|run|install-ssh|configure-wireguard|configure-ego-browser> [flags]")
}
