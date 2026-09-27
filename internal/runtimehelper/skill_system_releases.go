package runtimehelper

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/managedskills"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func parseSkillSystemPins(releases map[string]json.RawMessage) (skillmanager.SystemReleasePins, error) {
	var pins skillmanager.SystemReleasePins
	if len(releases) < 1 || len(releases) > 2 {
		return pins, errors.New("skill snapshot requires known system releases")
	}
	for name, data := range releases {
		switch name {
		case "ego-browser":
			if err := decodeSkillRelease(data, &pins.EgoBrowser, "version", "commit", "tree_sha256"); err != nil {
				return pins, err
			}
		case "agent-remote-device":
			if err := decodeSkillRelease(data, &pins.Device, "node_release_version", "protocol_version"); err != nil {
				return pins, err
			}
			if pins.Device == (skillmanager.DeviceSkillRelease{}) {
				return pins, errors.New("selected device skill has no release pin")
			}
		default:
			return pins, errors.New("unknown system skill release")
		}
	}
	return pins, pins.Validate()
}

func decodeSkillRelease(data []byte, target any, names ...string) error {
	if len(data) > 4096 || rejectDuplicateJSONKeys(data) != nil {
		return errors.New("invalid system skill release metadata")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != len(names) {
		return errors.New("incomplete system skill release metadata")
	}
	for _, name := range names {
		if fields[name] == nil || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return errors.New("missing system skill release field")
		}
	}
	return decodeStrictJSON(data, target)
}

func verifySkillSystemPins(spec SessionSpec, pins skillmanager.SystemReleasePins) error {
	if err := pins.Validate(); err != nil {
		return err
	}
	if pins.EgoBrowser != (skillmanager.EgoBrowserRelease{
		Version: egobrowserartifact.OfficialSkillVersion, Commit: egobrowserartifact.OfficialSkillSourceCommit,
		TreeSHA256: egobrowserartifact.OfficialSkillTreeSHA256,
	}) {
		return errors.New("reserved ego-browser skill differs from the Helper release")
	}
	if err := managedskills.VerifyEmbeddedEgoBrowser(); err != nil {
		return errors.New("embedded ego-browser skill failed release verification")
	}
	if spec.EgoBrowserEnabled {
		if spec.EgoBrowserSkillVersion != pins.EgoBrowser.Version || spec.EgoBrowserSkillTreeSHA256 != pins.EgoBrowser.TreeSHA256 {
			return errors.New("runtime ego-browser artifacts differ from the reserved skill")
		}
		if err := validateEgoBrowserArtifacts(spec.EgoBrowserWrapperPath, spec.EgoBrowserWrapperVersion, spec.EgoBrowserSkillPath, spec.EgoBrowserSkillVersion, spec.EgoBrowserSkillTreeSHA256); err != nil {
			return errors.New("reserved ego-browser runtime artifacts are unavailable")
		}
	}
	if spec.DeviceControlProtocolVersion == 0 {
		if pins.Device != (skillmanager.DeviceSkillRelease{}) {
			return errors.New("reserved device skill is absent from the trusted runtime")
		}
		return nil
	}
	// This is the Helper binary's build identity, not the configurable worker version string.
	if spec.DeviceControlProtocolVersion != 1 || pins.Device.ProtocolVersion != spec.DeviceControlProtocolVersion || pins.Device.NodeReleaseVersion != config.DefaultVersion {
		return errors.New("reserved device skill differs from the Helper release or runtime protocol")
	}
	if err := validateManagedDeviceProxy(spec.DeviceProxyPath); err != nil {
		return errors.New("reserved device proxy is unavailable")
	}
	return nil
}
