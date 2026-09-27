package runtimehelper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/api"
	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func deploymentEngineFixture(t *testing.T, input skillmanager.SkillDeployment) Engine {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned Linux skill store")
	}
	root := t.TempDir()
	engine := NewEngine(EngineConfig{NodeID: input.NodeID, StateRoot: filepath.Join(root, "runtime"),
		WorkspaceRoot: filepath.Join(root, "workspaces"), AccountRoot: filepath.Join(root, "accounts"), SkillStateRoot: filepath.Join(root, "skills")})
	store, err := engine.openSkillStateRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = skillmanager.CloseAccountImports(store, skillmanager.AccountFence{Version: 1, NodeID: input.NodeID,
		UserID: input.UserID, AccountID: input.AccountID, DirectoryEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestDeploymentPreparationLinuxHTTPToPrivateDirectoryWithoutSession(t *testing.T) {
	content := []byte(strings.Repeat("original instructions\n", 100_000))
	input := preparationTestDeployment(t, content)
	engine := deploymentEngineFixture(t, input)
	entry := input.Manifest.Entries[1]
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer disposable-token" || r.URL.Query().Get("task_id") != input.TaskID || r.URL.Query().Get("lease_attempt") != "3" {
			t.Error("deployment download lost exact authority")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		prefix := "/api/v1/node/skill-deployments/" + input.AttemptID
		switch r.URL.Path {
		case prefix:
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "status": "prepared_input", "committed": false, "errors": []any{}, "data": input})
		case prefix + "/files/" + entry.SHA256:
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
			w.Header().Set("ETag", `"`+entry.SHA256+`"`)
			_, _ = w.Write(content)
		default:
			t.Error("unexpected deployment route")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer httpServer.Close()
	control := api.NewClient(httpServer.URL, "disposable-token")
	received, err := control.GetSkillDeployment(context.Background(), input.SkillDeploymentIdentity, 3)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer("", -1, os.Geteuid(), engine)
	client := skillPreparationTestPeer(t, server.handle)
	downloads := 0
	receipt, err := client.PrepareSkillDeployment(context.Background(), "deployment_original", received, func(ctx context.Context, entry skillmanager.Entry, writer io.Writer) error {
		downloads++
		return control.ReadSkillDeploymentFile(ctx, input.SkillDeploymentIdentity, 3, entry, writer)
	})
	if err != nil || receipt.Validate(input) != nil || downloads != 1 {
		t.Fatal("HTTP to Helper preparation failed", err, downloads)
	}
	bundlePath := filepath.Join(engine.config.SkillStateRoot, "deployment-"+input.AttemptID)
	data, err := os.ReadFile(filepath.Join(bundlePath, "work/learning/SKILL.md"))
	if err != nil || !bytes.Equal(data, content) {
		t.Fatal("retained content differs", err)
	}
	for _, root := range []string{engine.config.StateRoot, engine.config.AccountRoot, engine.config.WorkspaceRoot} {
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("independent deployment touched session/account/workspace state", err)
		}
	}
	// A new Engine and socket must rediscover durable evidence without the original HTTP service.
	httpServer.Close()
	restarted := NewServer("", -1, os.Geteuid(), NewEngine(engine.config))
	second := skillPreparationTestPeer(t, restarted.handle)
	replay, err := second.PrepareSkillDeployment(context.Background(), "deployment_restarted", input, func(context.Context, skillmanager.Entry, io.Writer) error {
		t.Error("durable replay redownloaded content")
		return errors.New("unexpected download")
	})
	if err != nil || replay != receipt {
		t.Fatal("restart did not preserve immutable preparation", err)
	}
}

func TestDeploymentPreparationLinuxRejectsChangedOwnerAndMissingFenceBeforeBytes(t *testing.T) {
	for _, kind := range []string{"node", "owner", "account", "fence", "epoch"} {
		t.Run(kind, func(t *testing.T) {
			input := preparationTestDeployment(t, []byte("original"))
			engine := deploymentEngineFixture(t, input)
			switch kind {
			case "node":
				input.NodeID, input.Plan.NodeID = input.TaskID, input.TaskID
			case "owner":
				input.UserID, input.Plan.UserID = input.TaskID, input.TaskID
			case "account":
				input.AccountID, input.Plan.AccountID = input.TaskID, input.TaskID
			case "fence":
				if err := os.Remove(filepath.Join(engine.config.SkillStateRoot, "account-"+input.AccountID+".json")); err != nil {
					t.Fatal(err)
				}
			case "epoch":
				input.DirectoryEpoch = 0
			}
			input.PlanDigest, _ = input.Plan.Digest()
			server := NewServer("", -1, os.Geteuid(), engine)
			client := skillPreparationTestPeer(t, server.handle)
			if _, err := client.PrepareSkillDeployment(context.Background(), "deployment_invalid", input, func(context.Context, skillmanager.Entry, io.Writer) error {
				t.Error("invalid authority reached content")
				return errors.New("unexpected download")
			}); err == nil {
				t.Fatal("foreign or absent authority accepted")
			}
		})
	}
}

func TestDeploymentPreparationLinuxFailureCannotPublishPartialDirectory(t *testing.T) {
	for _, kind := range []string{"source_failure", "cancel", "bad_hash", "missing_ack"} {
		t.Run(kind, func(t *testing.T) {
			input := preparationTestDeployment(t, []byte("original"))
			engine := deploymentEngineFixture(t, input)
			server := NewServer("", -1, os.Geteuid(), engine)
			finished := make(chan struct{}, 1)
			client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) { server.handle(ctx, connection); finished <- struct{}{} })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "bad_hash" || kind == "missing_ack" {
				connection, err := net.Dial("unix", client.socketPath)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(input)
				payload, _ := Map(deploymentPreparationHeader{AttemptID: input.AttemptID, TaskID: input.TaskID, InputSize: int64(len(data))})
				_ = json.NewEncoder(connection).Encode(Request{Version: 1, RequestID: "deployment_bad_bytes", Operation: deploymentPreparationOperation, Payload: payload})
				reader := bufio.NewReader(connection)
				if frame, err := readDeploymentFrame(reader); err != nil || frame.Kind != "input" {
					t.Fatal("input handshake failed", err)
				}
				_, _ = connection.Write(data)
				if frame, err := readDeploymentFrame(reader); err != nil || frame.Kind != "object" {
					t.Fatal("object handshake failed", err)
				}
				if kind == "bad_hash" {
					_, _ = connection.Write([]byte("modified\x06"))
					if frame, err := readDeploymentFrame(reader); err == nil && frame.Kind == "prepared" {
						t.Fatal("Helper did not verify bytes")
					}
				} else {
					_, _ = connection.Write([]byte("original"))
				}
				_ = connection.Close()
			} else {
				_, err := client.PrepareSkillDeployment(ctx, "deployment_failed", input, func(_ context.Context, _ skillmanager.Entry, writer io.Writer) error {
					if _, err := io.WriteString(writer, "original"); err != nil {
						return err
					}
					if kind == "cancel" {
						cancel()
						return ctx.Err()
					}
					return errors.New("final source verification failed")
				})
				if err == nil {
					t.Fatal("failed source accepted")
				}
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled Helper stream remained alive")
			}
			entries, err := os.ReadDir(engine.config.SkillStateRoot)
			if err != nil || len(entries) != 1 || entries[0].Name() != "account-"+input.AccountID+".json" {
				t.Fatal("failed transfer left published or temporary content", entries, err)
			}
		})
	}
}

func TestDeploymentPreparationLinuxDisconnectCancelsSerializationWait(t *testing.T) {
	input := preparationTestDeployment(t, []byte("original"))
	engine := deploymentEngineFixture(t, input)
	server := NewServer("", -1, os.Geteuid(), engine)
	server.mu.Lock()
	defer server.mu.Unlock()
	done := make(chan struct{}, 1)
	client := skillPreparationTestPeer(t, func(ctx context.Context, connection net.Conn) { server.handle(ctx, connection); done <- struct{}{} })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.PrepareSkillDeployment(ctx, "deployment_waiting", input, func(context.Context, skillmanager.Entry, io.Writer) error {
		t.Error("locked helper requested bytes")
		return nil
	}); err == nil {
		t.Fatal("locked preparation unexpectedly completed")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected preparation kept waiting for mutation lock")
	}
}
