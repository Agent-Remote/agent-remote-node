package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/ledger"
	"github.com/Agent-Remote/agent-remote-node/internal/sshkeys"
	"github.com/Agent-Remote/agent-remote-node/internal/worker"
	"golang.org/x/net/idna"
	"golang.org/x/sys/unix"
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
	case "install":
		return installNode(args[1:])
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

func installNode(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "config path")
	serverURL := fs.String("server-url", "", "server URL")
	nodeID := fs.String("node-id", "", "node ID (optional when supplied by the join code)")
	version := fs.String("version", config.DefaultVersion, "node version")
	joinCodeStdin := fs.Bool("join-code-stdin", false, "read the one-time join code from stdin")
	exchangeID := fs.String("exchange-id", "", "resume an interrupted enrollment exchange")
	enableEgoBrowser := fs.Bool("enable-ego-browser", false, "honor an explicitly authorized ego-browser capability")
	disableEgoBrowser := fs.Bool("disable-ego-browser", false, "keep the ego-browser capability disabled")
	egoBrowserRuntimeRoot := fs.String("ego-browser-runtime-root", "/opt/agent-remote/ego-browser", "managed ego-browser runtime root")
	runtimeBackends := fs.String("runtime-backends", "", "comma-separated runtime backends")
	systemInstall := fs.Bool("system-install", false, "use system service paths")
	prefix := fs.String("prefix", "/usr/local", "system installation prefix")
	stateDir := fs.String("state-dir", "/var/lib/agent-remote-node", "system service state directory")
	dataDir := fs.String("data-dir", "/var/lib/agent-remote", "managed workspace and account data directory")
	claudeRuntimePath := fs.String("claude-runtime-path", "/opt/agent-remote/runtimes/claude/current/bin/claude", "managed Claude executable")
	deviceProxyPath := fs.String("device-proxy-path", "/opt/agent-remote/device/current/bin/agent-remote-device-proxy", "managed device proxy executable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *enableEgoBrowser && *disableEgoBrowser {
		return errors.New("--enable-ego-browser and --disable-ego-browser are mutually exclusive")
	}
	existing, hasExisting, err := loadInstallConfig(*configPath)
	if err != nil {
		return err
	}
	resolvedServerURL, err := resolveInstallServerURL(*serverURL, existing, hasExisting)
	if err != nil {
		return err
	}
	cfg := existing
	if !hasExisting {
		cfg = config.Config{SupportedToolTypes: []string{"claude"}}
	}
	if *runtimeBackends != "" {
		cfg.AllowedRuntimeBackends = splitCommaList(*runtimeBackends)
	}
	if *systemInstall {
		applySystemInstallPaths(&cfg, *prefix, *stateDir, *dataDir, *claudeRuntimePath, *deviceProxyPath)
	}
	// Verify an enabled release before consuming the one-time code.
	if (*enableEgoBrowser || (hasExisting && existing.EgoBrowserEnabled)) && !*disableEgoBrowser {
		if err := verifyInstallEgoBrowser(&cfg, *egoBrowserRuntimeRoot); err != nil {
			return errors.New("release_verification_failed")
		}
	}
	statePath := joinExchangeStatePath(*configPath)
	persisted, err := loadJoinExchangeState(statePath)
	if err != nil {
		return err
	}
	if persisted != nil {
		if persisted.ServerURL != resolvedServerURL {
			return errors.New("join exchange state belongs to a different server")
		}
		if *exchangeID != "" && *exchangeID != persisted.ExchangeID {
			return errors.New("exchange-id does not match the pending enrollment")
		}
		if persisted.NodeID != "" && *nodeID != "" && *nodeID != persisted.NodeID {
			return errors.New("node-id does not match the pending enrollment")
		}
		*exchangeID = persisted.ExchangeID
	}
	// Recovery persists only the exchange ID, never the join code.
	joinCode := ""
	if *joinCodeStdin {
		joinCode, err = readJoinCode()
		if err != nil {
			return err
		}
	} else if persisted == nil && *exchangeID == "" {
		return errors.New("--join-code-stdin or --exchange-id is required for enrollment")
	}
	if *exchangeID == "" {
		*exchangeID, err = newExchangeID()
		if err != nil {
			return err
		}
	}
	if err := validateExchangeID(*exchangeID); err != nil {
		return err
	}
	requestedNodeID := *nodeID
	if requestedNodeID == "" && persisted != nil {
		requestedNodeID = persisted.NodeID
	}
	if requestedNodeID == "" && hasExisting {
		requestedNodeID = existing.NodeID
	}
	requestedVersion := *version
	if hasExisting && !flagWasProvided(args, "--version") && existing.Version != "" {
		requestedVersion = existing.Version
	}
	if persisted == nil {
		if err := saveJoinExchangeState(statePath, joinExchangeState{
			Version: 1, ExchangeID: *exchangeID, ServerURL: resolvedServerURL,
			NodeID: requestedNodeID, CreatedAt: timeNowUnix(),
		}); err != nil {
			return err
		}
	}
	// The signed local release supplies hints; the Server validates the profile.
	var requestedEgoBrowserIntent *bool
	if *enableEgoBrowser || *disableEgoBrowser {
		value := *enableEgoBrowser
		requestedEgoBrowserIntent = &value
	}
	response, err := api.NewClient(resolvedServerURL, "").ExchangeJoinCode(context.Background(), api.JoinCodeExchangeRequest{
		NodeID:            requestedNodeID,
		Version:           requestedVersion,
		JoinCode:          joinCode,
		ExchangeID:        *exchangeID,
		EgoBrowserEnabled: requestedEgoBrowserIntent,
		WrapperVersion:    egobrowserartifact.PinnedWrapperVersion,
		SkillVersion:      egobrowserartifact.OfficialSkillVersion,
		RuntimeVersion:    requestedVersion,
		ArtifactDigest:    "sha256:" + egobrowserartifact.OfficialSkillTreeSHA256,
	})
	if err != nil {
		return err
	}
	if response.Data.NodeID == "" || response.Data.NodeToken == "" || response.Data.ExchangeID != *exchangeID {
		return errors.New("join-code exchange returned an incomplete node credential")
	}
	if err := validateJoinCodeExchangeProfile(response, resolvedServerURL, requestedVersion, requestedEgoBrowserIntent); err != nil {
		return err
	}
	cfg.ServerURL = resolvedServerURL
	cfg.NodeID = response.Data.NodeID
	cfg.NodeToken = response.Data.NodeToken
	cfg.Version = requestedVersion
	if len(cfg.SupportedToolTypes) == 0 {
		cfg.SupportedToolTypes = []string{"claude"}
	}
	// Reinstalls preserve intent; new nodes stay disabled without authorization.
	desiredEnabled := false
	if hasExisting {
		desiredEnabled = existing.EgoBrowserEnabled
	} else if response.Data.EgoBrowserIntent != nil && *response.Data.EgoBrowserIntent {
		desiredEnabled = true
	}
	if *enableEgoBrowser {
		if !response.Data.EgoBrowserEnabled {
			return errors.New("ego-browser enable was not authorized by the join code")
		}
		desiredEnabled = true
	}
	if *disableEgoBrowser {
		desiredEnabled = false
	}
	if desiredEnabled {
		if err := verifyInstallEgoBrowser(&cfg, *egoBrowserRuntimeRoot); err != nil {
			return errors.New("release_verification_failed")
		}
	}
	cfg.EgoBrowserEnabled = desiredEnabled
	if err := config.Save(*configPath, cfg); err != nil {
		return err
	}
	if err := clearJoinExchangeState(statePath); err != nil {
		return err
	}
	// Never print the join code or node token.
	fmt.Printf("installed node %s\n", response.Data.NodeID)
	return nil
}

func validateJoinCodeExchangeProfile(
	response api.JoinCodeExchangeResponse,
	serverURL string,
	requestedVersion string,
	requestedEgoBrowserIntent *bool,
) error {
	data := response.Data
	if responseServerURL(data.ServerOrigin) != responseServerURL(serverURL) {
		return errors.New("join-code exchange belongs to a different server")
	}
	if data.ReleaseProfile == "" || data.WrapperVersion == "" || data.SkillVersion == "" ||
		data.ArtifactDigest == "" || data.ProfileDigest == "" {
		return errors.New("join-code exchange returned incomplete release metadata")
	}
	if data.WrapperVersion != egobrowserartifact.PinnedWrapperVersion ||
		data.SkillVersion != egobrowserartifact.OfficialSkillVersion ||
		data.ArtifactDigest != "sha256:"+egobrowserartifact.OfficialSkillTreeSHA256 {
		return errors.New("join-code exchange release profile is not approved")
	}
	if data.RuntimeVersion != "" && data.RuntimeVersion != requestedVersion {
		return errors.New("join-code exchange runtime version does not match the requested version")
	}
	intent := "preserve"
	if data.EgoBrowserIntent != nil {
		// Applied intent may differ only when reinstalling with preserved state.
		if requestedEgoBrowserIntent != nil && data.EgoBrowserEnabled != *data.EgoBrowserIntent {
			return errors.New("join-code exchange ego-browser intent is inconsistent")
		}
		if requestedEgoBrowserIntent != nil && *requestedEgoBrowserIntent != *data.EgoBrowserIntent {
			return errors.New("join-code exchange ego-browser intent was not authorized")
		}
		if *data.EgoBrowserIntent {
			intent = "true"
		} else {
			intent = "false"
		}
	} else if requestedEgoBrowserIntent != nil {
		return errors.New("join-code exchange did not return an authorized ego-browser intent")
	}
	material := strings.Join([]string{
		"node-join-profile-v1",
		responseServerURL(data.ServerOrigin),
		data.NodeID,
		data.ReleaseProfile,
		data.WrapperVersion,
		data.SkillVersion,
		data.RuntimeVersion,
		data.ArtifactDigest,
		intent,
	}, "\x00")
	digest := sha256.Sum256([]byte(material))
	if hex.EncodeToString(digest[:]) != strings.ToLower(data.ProfileDigest) {
		return errors.New("join-code exchange profile digest is invalid")
	}
	return nil
}

func readJoinCode() (string, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 4097))
	if err != nil {
		return "", errors.New("failed to read join code from stdin")
	}
	if len(data) == 0 || len(data) > 4096 {
		return "", errors.New("join code is empty or too long")
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", errors.New("join code is invalid")
	}
	return value, nil
}

func newExchangeID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", errors.New("failed to create enrollment exchange ID")
	}
	return hex.EncodeToString(bytes[:]), nil
}

func responseServerURL(value string) string {
	canonical, err := canonicalizeOrigin(value)
	if err != nil {
		return ""
	}
	return canonical
}

func loadInstallConfig(path string) (config.Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.Config{}, false, nil
	}
	if err != nil {
		return config.Config{}, false, err
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config.Config{}, false, errors.New("existing node config is invalid")
	}
	cfg = cfg.WithDefaults()
	cfg.SourcePath = path
	return cfg, true, nil
}

func resolveInstallServerURL(requested string, existing config.Config, hasExisting bool) (string, error) {
	value := strings.TrimSpace(requested)
	if value == "" && hasExisting {
		value = existing.ServerURL
	}
	if value == "" {
		return "", errors.New("server_url is required when no existing node config is available")
	}
	canonical, err := canonicalizeOrigin(value)
	if err != nil {
		return "", errors.New("server_url is invalid")
	}
	return canonical, nil
}

// canonicalizeOrigin returns the unique scheme/host/port credential scope.
func canonicalizeOrigin(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", errors.New("origin is empty")
	}
	for _, character := range raw {
		if character < 0x20 || character == 0x7f || unicode.IsSpace(character) {
			return "", errors.New("origin contains whitespace or control characters")
		}
	}
	if strings.ContainsAny(raw, "?#\\") {
		return "", errors.New("origin contains query, fragment, or backslash")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("origin authority is invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Host == "" || parsed.Opaque != "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("origin is not a bare http(s) origin")
	}
	authority := parsed.Host
	if strings.ContainsAny(authority, "@/\\") || strings.ContainsAny(authority, "\x00\t\r\n") {
		return "", errors.New("origin authority is invalid")
	}

	var (
		hostText    string
		portText    string
		portPresent bool
		host        string
		ipLiteral   bool
	)
	if strings.HasPrefix(authority, "[") {
		closing := strings.IndexByte(authority, ']')
		if closing <= 1 || strings.Contains(authority[1:closing], "[") ||
			strings.Contains(authority[closing+1:], "]") {
			return "", errors.New("origin IPv6 authority is invalid")
		}
		hostText = authority[1:closing]
		suffix := authority[closing+1:]
		if suffix != "" {
			if !strings.HasPrefix(suffix, ":") {
				return "", errors.New("origin IPv6 authority is invalid")
			}
			portPresent = true
			portText = suffix[1:]
		}
		if strings.Contains(hostText, "%") {
			return "", errors.New("origin IPv6 zone is not allowed")
		}
		address, parseErr := netip.ParseAddr(hostText)
		if parseErr != nil || !address.Is6() {
			return "", errors.New("origin IPv6 address is invalid")
		}
		host = address.String()
		ipLiteral = true
	} else {
		colonCount := strings.Count(authority, ":")
		if colonCount > 1 {
			return "", errors.New("IPv6 origins must use brackets")
		}
		if colonCount == 1 {
			parts := strings.SplitN(authority, ":", 2)
			hostText, portText, portPresent = parts[0], parts[1], true
		} else {
			hostText = authority
		}
		if hostText == "" {
			return "", errors.New("origin host is empty")
		}
		// Remove one DNS root dot while leaving doubled dots invalid.
		hostText = strings.TrimSuffix(hostText, ".")
		if hostText == "" {
			return "", errors.New("origin host is empty")
		}
		if address, parseErr := netip.ParseAddr(hostText); parseErr == nil {
			if !address.Is4() {
				return "", errors.New("IPv6 origins must use brackets")
			}
			host = address.String()
			ipLiteral = true
		} else {
			asciiHost, idnaErr := idna.Lookup.ToASCII(hostText)
			if idnaErr != nil {
				return "", errors.New("origin host is invalid")
			}
			host = strings.ToLower(strings.TrimSuffix(asciiHost, "."))
			if len(host) == 0 || len(host) > 253 {
				return "", errors.New("origin host is invalid")
			}
			for _, label := range strings.Split(host, ".") {
				if len(label) == 0 || len(label) > 63 ||
					label[0] == '-' || label[len(label)-1] == '-' {
					return "", errors.New("origin host is invalid")
				}
				for _, character := range label {
					if !(character >= 'a' && character <= 'z') &&
						!(character >= 'A' && character <= 'Z') &&
						!(character >= '0' && character <= '9') && character != '-' {
						return "", errors.New("origin host is invalid")
					}
				}
			}
		}
	}

	var port *int
	if portPresent {
		if portText == "" || len(portText) > 5 {
			return "", errors.New("origin port is invalid")
		}
		for _, character := range portText {
			if character < '0' || character > '9' {
				return "", errors.New("origin port is invalid")
			}
		}
		parsedPort, parseErr := strconv.Atoi(portText)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", errors.New("origin port is invalid")
		}
		port = &parsedPort
	}
	if port == nil || (scheme == "http" && *port == 80) || (scheme == "https" && *port == 443) {
		port = nil
	}
	isLoopback := host == "localhost"
	if ipLiteral {
		address, parseErr := netip.ParseAddr(host)
		isLoopback = parseErr == nil && address.IsLoopback()
	}
	if scheme == "http" && !isLoopback {
		return "", errors.New("http origins are restricted to loopback")
	}
	hostPart := host
	if ipLiteral && strings.Contains(host, ":") {
		hostPart = "[" + host + "]"
	}
	if port != nil {
		return scheme + "://" + hostPart + ":" + strconv.Itoa(*port), nil
	}
	return scheme + "://" + hostPart, nil
}

func flagWasProvided(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func validateExchangeID(value string) error {
	if len(value) < 16 || len(value) > 128 {
		return errors.New("exchange-id is invalid")
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '-' && character != '_' {
			return errors.New("exchange-id is invalid")
		}
	}
	return nil
}

func joinExchangeStatePath(configPath string) string {
	return configPath + ".join-exchange.json"
}

func loadJoinExchangeState(path string) (*joinExchangeState, error) {
	directory, name, err := openJoinExchangeStateDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	file, _, err := openJoinExchangeStateFile(directory, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > 16<<10 {
		return nil, errors.New("join exchange state is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var state joinExchangeState
	if err := decoder.Decode(&state); err != nil || state.Version != 1 || state.ServerURL == "" ||
		state.CreatedAt <= 0 || validateExchangeID(state.ExchangeID) != nil {
		return nil, errors.New("join exchange state is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("join exchange state is invalid")
	}
	server, err := resolveInstallServerURL(state.ServerURL, config.Config{}, false)
	if err != nil || server != state.ServerURL {
		return nil, errors.New("join exchange state is invalid")
	}
	return &state, nil
}

func saveJoinExchangeState(path string, state joinExchangeState) error {
	if state.Version != 1 || state.ServerURL == "" || state.CreatedAt <= 0 ||
		validateExchangeID(state.ExchangeID) != nil {
		return errors.New("join exchange state is invalid")
	}
	server, err := resolveInstallServerURL(state.ServerURL, config.Config{}, false)
	if err != nil || server != state.ServerURL {
		return errors.New("join exchange state is invalid")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	directory, name, err := openJoinExchangeStateDirectory(path, true)
	if err != nil {
		return err
	}
	defer directory.Close()
	if existing, _, openErr := openJoinExchangeStateFile(directory, name); openErr == nil {
		if closeErr := existing.Close(); closeErr != nil {
			return closeErr
		}
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return openErr
	}
	temporary, temporaryName, err := createJoinExchangeStateTemporary(directory)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = unix.Unlinkat(int(directory.Fd()), temporaryName, 0)
		}
	}()
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := unix.Renameat(int(directory.Fd()), temporaryName, int(directory.Fd()), name); err != nil {
		return err
	}
	removeTemporary = false
	installed, installedInfo, err := openJoinExchangeStateFile(directory, name)
	if err != nil {
		return err
	}
	closeErr := installed.Close()
	if closeErr != nil {
		return closeErr
	}
	if installedInfo.Size() != int64(len(data)+1) {
		return errors.New("join exchange state path is unsafe")
	}
	return syncJoinExchangeStateDirectory(directory)
}

func clearJoinExchangeState(path string) error {
	directory, name, err := openJoinExchangeStateDirectory(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	file, _, err := openJoinExchangeStateFile(directory, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err := unix.Unlinkat(int(directory.Fd()), name, 0); err != nil {
		return err
	}
	return syncJoinExchangeStateDirectory(directory)
}

func openJoinExchangeStateDirectory(path string, create bool) (*os.File, string, error) {
	cleanPath := filepath.Clean(path)
	name := filepath.Base(cleanPath)
	if name == "." || name == string(filepath.Separator) || strings.Contains(name, string(filepath.Separator)) {
		return nil, "", errors.New("join exchange state path is unsafe")
	}
	directoryPath := filepath.Dir(cleanPath)
	if create {
		if err := os.MkdirAll(directoryPath, 0o700); err != nil {
			return nil, "", err
		}
	}
	before, err := os.Lstat(directoryPath)
	if err != nil {
		return nil, "", err
	}
	if err := validateJoinExchangeStateDirectory(before); err != nil {
		return nil, "", err
	}
	fd, err := unix.Open(directoryPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", errors.New("join exchange state parent is unsafe")
	}
	directory := os.NewFile(uintptr(fd), directoryPath)
	if directory == nil {
		_ = unix.Close(fd)
		return nil, "", errors.New("join exchange state parent is unsafe")
	}
	after, err := directory.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = directory.Close()
		return nil, "", errors.New("join exchange state parent changed while opening")
	}
	if err := validateJoinExchangeStateDirectory(after); err != nil {
		_ = directory.Close()
		return nil, "", err
	}
	return directory, name, nil
}

func validateJoinExchangeStateDirectory(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 ||
		stat.Uid != uint32(os.Geteuid()) {
		return errors.New("join exchange state parent is unsafe")
	}
	return nil
}

func openJoinExchangeStateFile(directory *os.File, name string) (*os.File, os.FileInfo, error) {
	path := filepath.Join(directory.Name(), name)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if err := validateJoinExchangeStateFile(before); err != nil {
		return nil, nil, err
	}
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, errors.New("join exchange state path is unsafe")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("join exchange state path is unsafe")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, nil, errors.New("join exchange state changed while opening")
	}
	if err := validateJoinExchangeStateFile(after); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, after, nil
}

func validateJoinExchangeStateFile(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("join exchange state path is unsafe")
	}
	return nil
}

func createJoinExchangeStateTemporary(directory *os.File) (*os.File, string, error) {
	for range 16 {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", errors.New("failed to create join exchange state")
		}
		name := ".agent-remote-join-exchange-" + hex.EncodeToString(random[:])
		fd, err := unix.Openat(
			int(directory.Fd()), name,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
			0o600,
		)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		file := os.NewFile(uintptr(fd), filepath.Join(directory.Name(), name))
		if file == nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(int(directory.Fd()), name, 0)
			return nil, "", errors.New("failed to create join exchange state")
		}
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			_ = unix.Unlinkat(int(directory.Fd()), name, 0)
			return nil, "", err
		}
		info, err := file.Stat()
		if err != nil || validateJoinExchangeStateFile(info) != nil {
			_ = file.Close()
			_ = unix.Unlinkat(int(directory.Fd()), name, 0)
			return nil, "", errors.New("join exchange state path is unsafe")
		}
		return file, name, nil
	}
	return nil, "", errors.New("failed to create join exchange state")
}

func syncJoinExchangeStateDirectory(directory *os.File) error {
	err := unix.Fsync(int(directory.Fd()))
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	return err
}

func timeNowUnix() int64 { return time.Now().Unix() }

func verifyInstallEgoBrowser(cfg *config.Config, runtimeRoot string) error {
	if runtimeRoot == "" {
		return errors.New("runtime root is missing")
	}
	metadata, err := readEgoBrowserRuntimeMetadata(runtimeRoot)
	if err != nil || metadata.WrapperVersion != egobrowserartifact.PinnedWrapperVersion ||
		metadata.SkillVersion != egobrowserartifact.OfficialSkillVersion {
		return errors.New("runtime metadata is not approved")
	}
	if err := egobrowserartifact.Verify(egobrowserartifact.RuntimeConfig{
		WrapperPath: metadata.WrapperPath, WrapperVersion: metadata.WrapperVersion,
		SkillPath: metadata.SkillPath, SkillVersion: metadata.SkillVersion,
		SkillTreeSHA256: metadata.SkillTreeSHA256,
	}); err != nil {
		return err
	}
	cfg.EgoBrowserWrapperPath = metadata.WrapperPath
	cfg.EgoBrowserWrapperVersion = metadata.WrapperVersion
	cfg.EgoBrowserSkillPath = metadata.SkillPath
	cfg.EgoBrowserSkillVersion = metadata.SkillVersion
	cfg.EgoBrowserSkillTreeSHA256 = metadata.SkillTreeSHA256
	cfg.EgoBrowserProtocolVersion = egobrowserartifact.PinnedProtocolVersion
	return nil
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

// joinExchangeState retains only non-secret enrollment recovery data.
type joinExchangeState struct {
	Version    int    `json:"version"`
	ExchangeID string `json:"exchange_id"`
	ServerURL  string `json:"server_url"`
	NodeID     string `json:"node_id,omitempty"`
	CreatedAt  int64  `json:"created_at"`
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
	// Stale artifact pins are accepted here only until the installed release is
	// verified and the updated configuration passes strict validation.
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
	cfg.EgoBrowserProtocolVersion = egobrowserartifact.PinnedProtocolVersion
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
	fmt.Println("agent-remote-node <register|install|heartbeat|poll-once|reconcile|run|install-ssh|configure-wireguard|configure-ego-browser> [flags]")
}
