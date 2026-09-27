package runtimehelper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func TestHelperFinalizationHoldSurvivesOriginalServerShutdown(t *testing.T) {
	engine, capture := finalizationTransferFixture(t)
	var held *os.File
	t.Run("original_server", func(t *testing.T) {
		client, _ := serveCaptureTest(t, engine, os.Getuid())
		var err error
		var record = capture
		held, record, err = client.HoldSkillFinalization(context.Background(), "hold-original", capture.Binding)
		if err != nil || held == nil || record != capture {
			t.Fatal("descriptor hold changed input", err)
		}
	})
	if held == nil {
		t.Fatal("original server produced no hold")
	}
	defer held.Close()
	manifest := filepath.Join(engine.config.SkillStateRoot, "session-"+capture.Binding.SessionID, "finalization/manifest.json")
	competitor, err := os.Open(manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer competitor.Close()
	if err := unix.Flock(int(competitor.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("server shutdown dropped client read hold")
	}
	var second *os.File
	t.Run("replacement_server", func(t *testing.T) {
		client, _ := serveCaptureTest(t, engine, os.Getuid())
		var err error
		second, _, err = client.HoldSkillFinalization(context.Background(), "hold-new-helper", capture.Binding)
		if err != nil {
			t.Fatal("new Helper instance could not share original lock", err)
		}
	})
	if second == nil {
		t.Fatal("replacement server produced no hold")
	}
	defer second.Close()
	_ = held.Close()
	if err := unix.Flock(int(competitor.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("closing one client released another client")
	}
	_ = second.Close()
	if err := unix.Flock(int(competitor.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal("closed readers leaked lock ownership", err)
	}
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	if file, _, err := client.HoldSkillFinalization(context.Background(), "hold-during-exclusive", capture.Binding); err == nil || file != nil {
		t.Fatal("exclusive reclamation admitted a reader")
	}
	_ = unix.Flock(int(competitor.Fd()), unix.LOCK_UN)
	foreign := capture.Binding
	foreign.NodeID = foreign.UserID
	if file, _, err := client.HoldSkillFinalization(context.Background(), "hold-foreign", foreign); err == nil || file != nil {
		t.Fatal("foreign identity acquired content hold")
	}
}

func TestHelperFinalizationHoldRejectsMalformedDescriptorFrame(t *testing.T) {
	_, capture := finalizationTransferFixture(t)
	expected := finalizationFileRequest{Binding: capture.Binding, Kind: "hold"}
	for _, mode := range []string{"valid", "empty", "kind", "binding", "entry", "descriptor_count"} {
		t.Run(mode, func(t *testing.T) {
			metadata := finalizationFileResponse{Record: capture, Kind: "hold", Size: 32}
			count := 1
			switch mode {
			case "empty":
				metadata.Size = 0
			case "kind":
				metadata.Kind = "manifest"
			case "binding":
				metadata.Record.Binding.DirectoryEpoch++
			case "entry":
				metadata.Entry = &skillmanager.Entry{}
			case "descriptor_count":
				count = 0
			}
			data, err := json.Marshal(struct {
				Version int                      `json:"version"`
				OK      bool                     `json:"ok"`
				Result  finalizationFileResponse `json:"result"`
			}{Version: ProtocolVersion, OK: true, Result: metadata})
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeFinalizationFileFrame(data, count, expected)
			if (err == nil) != (mode == "valid") {
				t.Fatal("malformed read hold metadata was accepted", err)
			}
		})
	}
}
