package runtimehelper

import (
	"context"
	"testing"
	"time"
)

func TestProbeCommandsBoundLifetimeAndOutput(t *testing.T) {
	for name, script := range map[string]string{
		"stalled":         "sleep 30",
		"descendant_pipe": "sleep 30 & exit 0",
		"oversized":       "head -c 70000 /dev/zero",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			started := time.Now()
			if _, err := runProbeCommand(ctx, "/bin/sh", "-c", script); err == nil {
				t.Fatal("unbounded or incomplete probe command accepted")
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("probe retained a stalled command or inherited pipe")
			}
		})
	}
}

func TestProbeQueueUsesCallerDeadline(t *testing.T) {
	server := NewServer("", -1, 0, NewEngine(EngineConfig{}))
	server.probeMu.Lock()
	defer server.probeMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if server.lockProbe(ctx) == nil {
		t.Fatal("cancelled probe waited for another observation")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if server.lockProbe(ctx) == nil || ctx.Err() == nil {
		t.Fatal("probe queue did not consume caller deadline")
	}
}

func TestProbeHonorsAlreadyCancelledObservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewEngine(EngineConfig{StateRoot: t.TempDir()}).probe(ctx); err == nil {
		t.Fatal("cancelled observation returned partial capability data")
	}
}
