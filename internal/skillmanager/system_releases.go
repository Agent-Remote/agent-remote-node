package skillmanager

import (
	"errors"
	"regexp"
)

// EgoBrowserRelease fixes the official skill's version, provenance and complete content tree.
type EgoBrowserRelease struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	TreeSHA256 string `json:"tree_sha256"`
}

// DeviceSkillRelease fixes the Node release containing the embedded device skill and its protocol.
type DeviceSkillRelease struct {
	NodeReleaseVersion string `json:"node_release_version"`
	ProtocolVersion    int    `json:"protocol_version"`
}

// SystemReleasePins retains comparable system identities independently of transient session specs.
// The zero value is readable only for historical recovery, never new preparation or launch.
type SystemReleasePins struct {
	EgoBrowser EgoBrowserRelease  `json:"ego_browser"`
	Device     DeviceSkillRelease `json:"device"`
}

var skillReleaseVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)
var skillReleaseCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Validate rejects incomplete pins while allowing an unselected device capability.
func (p SystemReleasePins) Validate() error {
	if !skillReleaseVersion.MatchString(p.EgoBrowser.Version) || !skillReleaseCommit.MatchString(p.EgoBrowser.Commit) || !contentDigestPattern.MatchString(p.EgoBrowser.TreeSHA256) {
		return errors.New("invalid ego-browser skill release pin")
	}
	if p.Device != (DeviceSkillRelease{}) && (!skillReleaseVersion.MatchString(p.Device.NodeReleaseVersion) || p.Device.ProtocolVersion != 1) {
		return errors.New("invalid device skill release pin")
	}
	return nil
}
