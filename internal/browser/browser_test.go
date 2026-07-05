package browser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartCreatesTemporaryProfileWithoutDocker(t *testing.T) {
	root := t.TempDir()
	payload := CreatePayload{
		BrowserSessionID: "bsess_123",
		UserID:           "user_1",
		TargetURL:        "https://claude.ai",
		RegionCode:       "US",
		Timezone:         "America/Los_Angeles",
		Locale:           "en_US.UTF-8",
		TTLSeconds:       900,
		ContainerName:    "agent-remote-browser-bsess123",
	}
	payload.Browser.Image = "kasmweb/chrome:1.18.0"
	payload.Browser.Mode = "incognito"
	payload.NetworkPolicy.DisableWebRTCLocalIP = true

	result, err := Start(root, "agent-remote-missing-docker", "kasmweb/chrome:1.18.0", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	if result.StreamEndpoint != "node-local://browser/bsess_123" {
		t.Fatalf("unexpected stream endpoint: %s", result.StreamEndpoint)
	}
	if _, err := os.Stat(filepath.Join(result.ProfilePath, "agent-remote-browser.json")); err != nil {
		t.Fatal(err)
	}
}

func TestStopRemovesTemporaryProfileWithoutDocker(t *testing.T) {
	root := t.TempDir()
	payload := CreatePayload{
		BrowserSessionID: "bsess_123",
		UserID:           "user_1",
		RegionCode:       "US",
		Timezone:         "America/Los_Angeles",
		Locale:           "en_US.UTF-8",
		ContainerName:    "agent-remote-browser-bsess123",
	}
	result, err := Start(root, "agent-remote-missing-docker", "kasmweb/chrome:1.18.0", "", payload)
	if err != nil {
		t.Fatal(err)
	}
	stopResult, err := Stop(root, "agent-remote-missing-docker", StopPayload{
		BrowserSessionID: "bsess_123",
		ContainerName:    "agent-remote-browser-bsess123",
		Reason:           "user_requested",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stopResult.ProfileRemoved {
		t.Fatal("expected profile to be removed")
	}
	if _, err := os.Stat(result.ProfilePath); !os.IsNotExist(err) {
		t.Fatalf("expected profile directory to be removed, got %v", err)
	}
}
