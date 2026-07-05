package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BrowserOptions describes browser runtime settings in create_browser_session.
type BrowserOptions struct {
	Image    string `json:"image"`
	Engine   string `json:"engine"`
	Mode     string `json:"mode"`
	Viewport struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"viewport"`
}

// NetworkPolicy describes browser egress constraints.
type NetworkPolicy struct {
	Egress               string `json:"egress"`
	DenyPrivateNetworks  bool   `json:"deny_private_networks"`
	DenyMetadataService  bool   `json:"deny_metadata_service"`
	DisableWebRTCLocalIP bool   `json:"disable_webrtc_local_ip"`
}

// CreatePayload describes a create_browser_session task payload.
type CreatePayload struct {
	BrowserSessionID string         `json:"browser_session_id"`
	UserID           string         `json:"user_id"`
	ToolAccountID    *string        `json:"tool_account_id"`
	TargetURL        string         `json:"target_url"`
	RegionCode       string         `json:"region_code"`
	Timezone         string         `json:"timezone"`
	Locale           string         `json:"locale"`
	TTLSeconds       int            `json:"ttl_seconds"`
	ContainerName    string         `json:"container_name"`
	Browser          BrowserOptions `json:"browser"`
	NetworkPolicy    NetworkPolicy  `json:"network_policy"`
}

// CreateResult describes a prepared browser runtime.
type CreateResult struct {
	Status           string `json:"status"`
	BrowserSessionID string `json:"browser_session_id"`
	ContainerID      string `json:"container_id"`
	ContainerName    string `json:"container_name"`
	StreamEndpoint   string `json:"stream_endpoint"`
	ProfilePath      string `json:"profile_path"`
}

// StopPayload describes a stop_browser_session task payload.
type StopPayload struct {
	BrowserSessionID string `json:"browser_session_id"`
	ContainerName    string `json:"container_name"`
	Reason           string `json:"reason"`
}

// StopResult describes stopped browser resources.
type StopResult struct {
	Status           string `json:"status"`
	BrowserSessionID string `json:"browser_session_id"`
	ContainerName    string `json:"container_name"`
	ContainerRemoved bool   `json:"container_removed"`
	ProfileRemoved   bool   `json:"profile_removed"`
}

// DecodeCreatePayload converts a generic task payload into a typed payload.
func DecodeCreatePayload(payload map[string]any) (CreatePayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return CreatePayload{}, err
	}
	var decoded CreatePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return CreatePayload{}, err
	}
	if decoded.BrowserSessionID == "" {
		return CreatePayload{}, errors.New("browser_session_id is required")
	}
	if decoded.UserID == "" {
		return CreatePayload{}, errors.New("user_id is required")
	}
	if decoded.RegionCode == "" {
		return CreatePayload{}, errors.New("region_code is required")
	}
	if decoded.Timezone == "" {
		return CreatePayload{}, errors.New("timezone is required")
	}
	if decoded.Locale == "" {
		return CreatePayload{}, errors.New("locale is required")
	}
	if decoded.ContainerName == "" {
		decoded.ContainerName = "agent-remote-browser-" + safeID(decoded.BrowserSessionID)
	}
	return decoded, nil
}

// DecodeStopPayload converts a generic task payload into a typed payload.
func DecodeStopPayload(payload map[string]any) (StopPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return StopPayload{}, err
	}
	var decoded StopPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return StopPayload{}, err
	}
	if decoded.BrowserSessionID == "" {
		return StopPayload{}, errors.New("browser_session_id is required")
	}
	if decoded.ContainerName == "" {
		decoded.ContainerName = "agent-remote-browser-" + safeID(decoded.BrowserSessionID)
	}
	return decoded, nil
}

// Start creates an incognito browser runtime without mounting workspace or account data.
func Start(browserRoot string, dockerBinary string, defaultImage string, browserPublicBaseURL string, payload CreatePayload) (CreateResult, error) {
	profilePath, err := sessionProfilePath(browserRoot, payload.BrowserSessionID)
	if err != nil {
		return CreateResult{}, err
	}
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return CreateResult{}, err
	}
	markerPath := filepath.Join(profilePath, "agent-remote-browser.json")
	if err := writeMarker(markerPath, payload); err != nil {
		return CreateResult{}, err
	}
	image := payload.Browser.Image
	if image == "" {
		image = defaultImage
	}
	containerID := payload.ContainerName
	streamEndpoint := "node-local://browser/" + payload.BrowserSessionID
	if _, err := exec.LookPath(dockerBinary); err == nil {
		startedID, mappedEndpoint, err := startContainer(dockerBinary, image, profilePath, browserPublicBaseURL, payload)
		if err != nil {
			return CreateResult{}, err
		}
		if startedID != "" {
			containerID = startedID
		}
		if mappedEndpoint != "" {
			streamEndpoint = mappedEndpoint
		}
	}
	return CreateResult{
		Status:           "ready",
		BrowserSessionID: payload.BrowserSessionID,
		ContainerID:      containerID,
		ContainerName:    payload.ContainerName,
		StreamEndpoint:   streamEndpoint,
		ProfilePath:      profilePath,
	}, nil
}

// Stop removes the browser container and temporary profile directory.
func Stop(browserRoot string, dockerBinary string, payload StopPayload) (StopResult, error) {
	containerRemoved := false
	if _, err := exec.LookPath(dockerBinary); err == nil {
		cmd := exec.Command(dockerBinary, "rm", "-f", payload.ContainerName)
		if output, err := cmd.CombinedOutput(); err == nil || isContainerMissing(string(output)) {
			containerRemoved = true
		} else {
			return StopResult{}, fmt.Errorf("docker rm failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	profileRemoved := false
	profilePath, err := sessionProfilePath(browserRoot, payload.BrowserSessionID)
	if err != nil {
		return StopResult{}, err
	}
	if err := os.RemoveAll(profilePath); err != nil {
		return StopResult{}, err
	}
	profileRemoved = true
	return StopResult{
		Status:           "stopped",
		BrowserSessionID: payload.BrowserSessionID,
		ContainerName:    payload.ContainerName,
		ContainerRemoved: containerRemoved,
		ProfileRemoved:   profileRemoved,
	}, nil
}

func startContainer(dockerBinary string, image string, profilePath string, browserPublicBaseURL string, payload CreatePayload) (string, string, error) {
	args := []string{
		"run",
		"--rm",
		"-d",
		"--name", payload.ContainerName,
		"-e", "TZ=" + payload.Timezone,
		"-e", "LANG=" + payload.Locale,
		"-e", "LC_ALL=" + payload.Locale,
		"-e", "LAUNCH_URL=" + payload.TargetURL,
		"-e", "APP_ARGS=" + chromeArgs(payload),
		"-e", "VNC_PW=" + temporaryPassword(payload.BrowserSessionID),
		"-v", profilePath + ":/tmp/agent-remote-browser-profile",
		"-p", "127.0.0.1::6901",
		image,
	}
	cmd := exec.Command(dockerBinary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "already in use") || strings.Contains(string(output), "is already in use") {
			return payload.ContainerName, browserEndpoint(dockerBinary, browserPublicBaseURL, payload), nil
		}
		return "", "", fmt.Errorf("docker run failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return lastOutputLine(string(output)), browserEndpoint(dockerBinary, browserPublicBaseURL, payload), nil
}

func writeMarker(path string, payload CreatePayload) error {
	marker := map[string]any{
		"browser_session_id": payload.BrowserSessionID,
		"user_id":            payload.UserID,
		"region_code":        payload.RegionCode,
		"timezone":           payload.Timezone,
		"locale":             payload.Locale,
		"container_name":     payload.ContainerName,
		"target_url_host":    safeTargetSummary(payload.TargetURL),
		"created_at":         time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func sessionProfilePath(browserRoot string, browserSessionID string) (string, error) {
	root := filepath.Clean(browserRoot)
	path := filepath.Join(root, safeID(browserSessionID))
	if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("browser profile path is outside root %s", root)
	}
	return path, nil
}

func safeID(value string) string {
	replacer := strings.NewReplacer("-", "", "_", "")
	cleaned := replacer.Replace(value)
	if len(cleaned) > 24 {
		return cleaned[:24]
	}
	return cleaned
}

func safeTargetSummary(value string) string {
	parts := strings.SplitN(value, "?", 2)
	return parts[0]
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func browserEndpoint(dockerBinary string, browserPublicBaseURL string, payload CreatePayload) string {
	if browserPublicBaseURL != "" {
		return strings.TrimRight(browserPublicBaseURL, "/")
	}
	cmd := exec.Command(dockerBinary, "port", payload.ContainerName, "6901/tcp")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	endpoint := strings.TrimSpace(string(output))
	if endpoint == "" {
		return ""
	}
	if strings.HasPrefix(endpoint, "0.0.0.0:") {
		endpoint = "127.0.0.1:" + strings.TrimPrefix(endpoint, "0.0.0.0:")
	}
	return "https://kasm_user:" + temporaryPassword(payload.BrowserSessionID) + "@" + endpoint + "/"
}

func lastOutputLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func chromeArgs(payload CreatePayload) string {
	args := []string{
		"--incognito",
		"--no-first-run",
		"--no-default-browser-check",
		"--lang=" + browserLanguage(payload.Locale),
	}
	if payload.NetworkPolicy.DisableWebRTCLocalIP {
		args = append(args, "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
	}
	if payload.Browser.Viewport.Width > 0 && payload.Browser.Viewport.Height > 0 {
		args = append(args, "--window-size="+strconv.Itoa(payload.Browser.Viewport.Width)+","+strconv.Itoa(payload.Browser.Viewport.Height))
	}
	return strings.Join(args, " ")
}

func browserLanguage(locale string) string {
	parts := strings.Split(locale, ".")
	base := strings.ReplaceAll(parts[0], "_", "-")
	if base == "" {
		return "en-US"
	}
	return base
}

func temporaryPassword(browserSessionID string) string {
	return "ar-" + safeID(browserSessionID)
}

func isContainerMissing(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no such") || strings.Contains(lower, "not found")
}
