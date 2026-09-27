package runtimehelper

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func TestDeploymentDrainLinuxWaitsForPreparationThenFencesRestart(t *testing.T) {
	input := preparationTestDeployment(t, []byte("original"))
	engine := deploymentEngineFixture(t, input)
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	copying := make(chan struct{})
	release := make(chan struct{})
	preparation := make(chan error, 1)
	go func() {
		_, err := client.PrepareSkillDeployment(context.Background(), "prepare_original", input, func(ctx context.Context, _ skillmanager.Entry, writer io.Writer) error {
			close(copying)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := io.WriteString(writer, "original")
			return err
		})
		preparation <- err
	}()
	select {
	case <-copying:
	case <-time.After(3 * time.Second):
		t.Fatal("copy never started")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	_, err := client.DrainSkillDeployment(ctx, "drain_waiting", input.SkillDeploymentIdentity)
	cancel()
	if err == nil {
		t.Fatal("drain acknowledged while original copy was still active")
	}
	if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "deployment-drain-"+input.AttemptID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("waiting cancellation sealed an active attempt", err)
	}
	close(release)
	select {
	case err := <-preparation:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not end")
	}
	receipt, err := client.DrainSkillDeployment(context.Background(), "drain_after_copy", input.SkillDeploymentIdentity)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer("", -1, os.Geteuid(), NewEngine(engine.config))
	again := skillPreparationTestPeer(t, restarted.handle)
	replay, err := again.DrainSkillDeployment(context.Background(), "drain_restart", input.SkillDeploymentIdentity)
	if err != nil || replay != receipt {
		t.Fatal("restart lost original drain", err)
	}
	if _, err := again.PrepareSkillDeployment(context.Background(), "prepare_delayed", input, func(context.Context, skillmanager.Entry, io.Writer) error {
		t.Error("drained attempt fetched bytes")
		return nil
	}); err == nil {
		t.Fatal("restart allowed delayed preparation")
	}
	bytes, err := os.ReadFile(filepath.Join(engine.config.SkillStateRoot, "deployment-"+input.AttemptID, "work/learning/SKILL.md"))
	if err != nil || string(bytes) != "original" {
		t.Fatal("drain changed retained bytes", err)
	}
}

func TestDeploymentDrainLinuxLostResponseRetainsFenceAndExactReceipt(t *testing.T) {
	input := preparationTestDeployment(t, []byte("original"))
	engine := deploymentEngineFixture(t, input)
	server := NewServer("", -1, os.Geteuid(), engine)
	// A proxy drops the reply only after the real authenticated handler completed its durable write.
	actual := skillPreparationTestPeer(t, server.handle)
	lost := skillPreparationTestPeer(t, func(_ context.Context, connection net.Conn) {
		request, err := readBoundedLine(bufio.NewReader(connection), 4096)
		if err != nil {
			t.Error(err)
			return
		}
		upstream, err := net.Dial("unix", actual.socketPath)
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		if _, err := upstream.Write(request); err != nil {
			t.Error(err)
			return
		}
		response, err := readHelperResponse(upstream)
		if err != nil || !response.OK {
			t.Error("real drain failed before response loss", err)
		}
	})
	if _, err := lost.DrainSkillDeployment(context.Background(), "drain_lost", input.SkillDeploymentIdentity); err == nil {
		t.Fatal("dropped response was accepted")
	}
	store, err := engine.openExistingSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := skillmanager.ReadDeploymentDrain(store, input.SkillDeploymentIdentity)
	if err != nil {
		t.Fatal("drain was not durably saved", err)
	}
	restarted := NewServer("", -1, os.Geteuid(), NewEngine(engine.config))
	client := skillPreparationTestPeer(t, restarted.handle)
	recovered, err := client.DrainSkillDeployment(context.Background(), "drain_recover", input.SkillDeploymentIdentity)
	if err != nil || recovered != saved {
		t.Fatal("lost response recovery changed fence", err)
	}
	if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "deployment-"+input.AttemptID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("drain created content", err)
	}
}

func TestDeploymentDrainLinuxDisconnectCancelsMutationWait(t *testing.T) {
	input := preparationTestDeployment(t, []byte("original"))
	engine := deploymentEngineFixture(t, input)
	server := NewServer("", -1, os.Geteuid(), engine)
	server.mu.Lock()
	defer server.mu.Unlock()
	done := make(chan struct{}, 1)
	client := skillPreparationTestPeer(t, func(ctx context.Context, c net.Conn) { server.handle(ctx, c); done <- struct{}{} })
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := client.DrainSkillDeployment(ctx, "drain_cancelled", input.SkillDeploymentIdentity); err == nil {
		t.Fatal("locked drain completed")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled drain retained a lock waiter")
	}
	if _, err := os.Lstat(filepath.Join(engine.config.SkillStateRoot, "deployment-drain-"+input.AttemptID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled drain created a fence", err)
	}
}
