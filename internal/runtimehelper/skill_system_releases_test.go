package runtimehelper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/config"
	"github.com/Agent-Remote/agent-remote-node/internal/egobrowserartifact"
	"github.com/Agent-Remote/agent-remote-node/internal/managedskills"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestSkillSystemPinsRequireCompleteCanonicalReferences(t *testing.T) {
	ego, err := json.Marshal(testSkillSystemPins().EgoBrowser)
	if err != nil {
		t.Fatal(err)
	}
	if pins, err := parseSkillSystemPins(map[string]json.RawMessage{"ego-browser": ego}); err != nil || pins != testSkillSystemPins() {
		t.Fatal("valid pinned reference rejected", err)
	}
	for _, raw := range []string{
		`null`, `{}`, `{"version":null}`, strings.Replace(string(ego), `"version"`, `"Version"`, 1),
		strings.Replace(string(ego), `"version":`, `"version":"duplicate","version":`, 1),
		strings.Replace(string(ego), `"commit":`, `"unknown":true,"commit":`, 1),
		strings.Replace(string(ego), egobrowserartifact.OfficialSkillSourceCommit, strings.Repeat("F", 40), 1),
		strings.Repeat(" ", 4096) + string(ego),
	} {
		if _, err := parseSkillSystemPins(map[string]json.RawMessage{"ego-browser": json.RawMessage(raw)}); err == nil {
			t.Fatal("malformed system reference accepted")
		}
	}
	for _, releases := range []map[string]json.RawMessage{
		nil, {}, {"other": ego}, {"Ego-browser": ego}, {"ego-browser": ego, "unrecognized": ego},
		{"ego-browser": ego, "agent-remote-device": json.RawMessage(`{"node_release_version":"one","protocol_version":1.0}`)},
		{"ego-browser": ego, "agent-remote-device": json.RawMessage(`{"node_release_version":"one","protocol_version":true}`)},
		{"ego-browser": ego, "agent-remote-device": json.RawMessage(`{"node_release_version":"one","protocol_version":null}`)},
		{"ego-browser": ego, "agent-remote-device": json.RawMessage(`{"node_release_version":"","protocol_version":0}`)},
		{"ego-browser": ego, "agent-remote-device": json.RawMessage(`{"node_release_version":"one","protocol_version":2}`)},
	} {
		if _, err := parseSkillSystemPins(releases); err == nil {
			t.Fatal("missing, unknown or invalid system selection accepted")
		}
	}
}

func TestSkillSystemPinsVerifyEmbeddedReleaseAndExactDeviceSelection(t *testing.T) {
	pins := testSkillSystemPins()
	if err := verifySkillSystemPins(SessionSpec{}, pins); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*skillmanager.SystemReleasePins){
		func(p *skillmanager.SystemReleasePins) { p.EgoBrowser.Version = "old" },
		func(p *skillmanager.SystemReleasePins) { p.EgoBrowser.Commit = strings.Repeat("0", 40) },
		func(p *skillmanager.SystemReleasePins) { p.EgoBrowser.TreeSHA256 = strings.Repeat("0", 64) },
	} {
		changed := pins
		change(&changed)
		if err := verifySkillSystemPins(SessionSpec{}, changed); err == nil {
			t.Fatal("different embedded release accepted")
		}
	}
	proxy := writeTestCommand(t, "proxy", "exit 0")
	spec := SessionSpec{DeviceControlProtocolVersion: 1, DeviceProxyPath: proxy}
	if err := verifySkillSystemPins(spec, pins); err == nil {
		t.Fatal("device runtime accepted an unpinned skill")
	}
	pins.Device = skillmanager.DeviceSkillRelease{NodeReleaseVersion: config.DefaultVersion, ProtocolVersion: 1}
	if err := verifySkillSystemPins(spec, pins); err != nil {
		t.Fatal("matching device release failed", err)
	}
	if err := verifySkillSystemPins(SessionSpec{}, pins); err == nil {
		t.Fatal("selected device skill disappeared from runtime")
	}
	pins.Device.NodeReleaseVersion = "different-node-release"
	if err := verifySkillSystemPins(spec, pins); err == nil {
		t.Fatal("different Helper build supplied device skill")
	}
	pins.Device.NodeReleaseVersion = config.DefaultVersion
	if err := os.Remove(proxy); err != nil {
		t.Fatal(err)
	}
	if err := verifySkillSystemPins(spec, pins); err == nil {
		t.Fatal("missing selected device proxy accepted")
	}
}

func pinnedSkillRuntimeFixture(t *testing.T) SessionSpec {
	t.Helper()
	root := t.TempDir()
	if err := managedskills.InstallClaude(root, nil); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(root, "releases", egobrowserartifact.PinnedWrapperVersion)
	for _, directory := range []string{filepath.Join(release, "bin"), filepath.Join(release, "skill")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	skillPath := filepath.Join(release, "skill", "ego-browser")
	if err := os.Rename(filepath.Join(root, ".claude", "skills", "ego-browser"), skillPath); err != nil {
		t.Fatal(err)
	}
	wrapper := []byte("#!/bin/sh\nexit 0\n")
	wrapperDigest := sha256.Sum256(wrapper)
	manifest, err := json.Marshal(map[string]any{
		"schema_version": 1, "name": egobrowserartifact.OfficialSkillName, "version": egobrowserartifact.OfficialSkillVersion,
		"upstream_repository": egobrowserartifact.OfficialSkillUpstreamRepository, "upstream_tag": "v" + egobrowserartifact.OfficialSkillVersion,
		"upstream_commit": egobrowserartifact.OfficialSkillSourceCommit, "skill_document_sha256": egobrowserartifact.OfficialSkillDocumentSHA256,
		"tree_sha256": egobrowserartifact.OfficialSkillTreeSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifest)
	for name, data := range map[string][]byte{
		"bin/ego-browser": wrapper, "VERSION": []byte(egobrowserartifact.PinnedWrapperVersion),
		"SKILL_VERSION": []byte(egobrowserartifact.OfficialSkillVersion), "WRAPPER_SHA256": []byte(hex.EncodeToString(wrapperDigest[:])),
		"SKILL_TREE_SHA256": []byte(egobrowserartifact.OfficialSkillTreeSHA256), "ego-browser-skill-source.json": manifest,
		"SOURCE_MANIFEST_SHA256": []byte(hex.EncodeToString(manifestDigest[:])),
	} {
		if err := os.WriteFile(filepath.Join(release, name), data, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	return SessionSpec{EgoBrowserEnabled: true, EgoBrowserWrapperPath: filepath.Join(release, "bin", "ego-browser"),
		EgoBrowserWrapperVersion: egobrowserartifact.PinnedWrapperVersion, EgoBrowserSkillPath: skillPath,
		EgoBrowserSkillVersion: egobrowserartifact.OfficialSkillVersion, EgoBrowserSkillTreeSHA256: egobrowserartifact.OfficialSkillTreeSHA256,
	}
}

func TestSkillSystemPinsVerifyEnabledRuntimeArtifactBytes(t *testing.T) {
	spec := pinnedSkillRuntimeFixture(t)
	if err := verifySkillSystemPins(spec, testSkillSystemPins()); err != nil {
		t.Fatal("verified runtime artifact rejected", err)
	}
	changed := spec
	changed.EgoBrowserSkillVersion = "different"
	if err := verifySkillSystemPins(changed, testSkillSystemPins()); err == nil {
		t.Fatal("runtime spec ignored original skill version")
	}
	if err := os.WriteFile(filepath.Join(spec.EgoBrowserSkillPath, "SKILL.md"), []byte("replaced artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifySkillSystemPins(spec, testSkillSystemPins()); err == nil {
		t.Fatal("replaced skill content accepted")
	}
}
