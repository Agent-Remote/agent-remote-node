package wireguard

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

const maxPeers = 4096

var interfacePattern = regexp.MustCompile(`^[A-Za-z0-9_=+.-]{1,15}$`)

// Peer is a validated WireGuard peer received from the control plane.
type Peer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
}

// SyncPayload is the declarative peer set accepted by the privileged helper.
type SyncPayload struct {
	Peers []Peer `json:"peers"`
}

// DecodeSyncPayload converts and validates a generic runtime-helper payload.
func DecodeSyncPayload(payload map[string]any) (SyncPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return SyncPayload{}, errors.New("wireguard payload is invalid")
	}
	var decoded SyncPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		return SyncPayload{}, errors.New("wireguard payload is invalid")
	}
	if len(decoded.Peers) > maxPeers {
		return SyncPayload{}, fmt.Errorf("wireguard payload exceeds %d peers", maxPeers)
	}
	seenKeys := make(map[string]bool, len(decoded.Peers))
	seenIPs := make(map[string]bool, len(decoded.Peers))
	for peerIndex := range decoded.Peers {
		peer := &decoded.Peers[peerIndex]
		if err := ValidateKey(peer.PublicKey); err != nil {
			return SyncPayload{}, fmt.Errorf("wireguard peer public key is invalid: %w", err)
		}
		if seenKeys[peer.PublicKey] {
			return SyncPayload{}, errors.New("wireguard peer public key is duplicated")
		}
		seenKeys[peer.PublicKey] = true
		if len(peer.AllowedIPs) == 0 {
			return SyncPayload{}, errors.New("wireguard peer requires an allowed IP")
		}
		for ipIndex, value := range peer.AllowedIPs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 || prefix != prefix.Masked() {
				return SyncPayload{}, errors.New("wireguard allowed IP must be a canonical IPv4 /32 prefix")
			}
			normalized := prefix.String()
			if seenIPs[normalized] {
				return SyncPayload{}, errors.New("wireguard allowed IP is duplicated")
			}
			seenIPs[normalized] = true
			peer.AllowedIPs[ipIndex] = normalized
		}
		sort.Strings(peer.AllowedIPs)
	}
	sort.Slice(decoded.Peers, func(i, j int) bool {
		return decoded.Peers[i].PublicKey < decoded.Peers[j].PublicKey
	})
	return decoded, nil
}

// ValidateKey accepts only canonical base64-encoded 32-byte WireGuard keys.
func ValidateKey(value string) error {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != value {
		return errors.New("expected a canonical base64-encoded 32-byte key")
	}
	return nil
}

// RenderSyncConfig renders the complete private configuration consumed by wg syncconf.
func RenderSyncConfig(privateKey string, listenPort int, payload SyncPayload) (string, error) {
	if err := ValidateKey(strings.TrimSpace(privateKey)); err != nil {
		return "", fmt.Errorf("wireguard private key is invalid: %w", err)
	}
	if listenPort < 1 || listenPort > 65535 {
		return "", errors.New("wireguard listen port is invalid")
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "[Interface]\nPrivateKey = %s\nListenPort = %d\n", strings.TrimSpace(privateKey), listenPort)
	for _, peer := range payload.Peers {
		fmt.Fprintf(&rendered, "\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\n", peer.PublicKey, strings.Join(peer.AllowedIPs, ", "))
	}
	return rendered.String(), nil
}

// ValidateInterface rejects values that cannot be Linux interface names.
func ValidateInterface(value string) error {
	if !interfacePattern.MatchString(value) {
		return errors.New("wireguard interface is invalid")
	}
	return nil
}
