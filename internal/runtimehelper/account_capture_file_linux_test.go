package runtimehelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func captureTransferFixture(t *testing.T) (Engine, skillmanager.AccountCapture) {
	t.Helper()
	engine, binding, _ := accountTakeoverFixture(t)
	source := filepath.Join(engine.config.AccountRoot, binding.UserID, "tool-accounts", "claude", binding.AccountID, ".claude", "skills")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "state"), []byte("retained\x00\xff"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err := engine.captureNativeAccountTakeover(context.Background(), binding, nil)
	if err != nil {
		t.Fatal(err)
	}
	return engine, capture
}

func serveCaptureTest(t *testing.T, engine Engine, allowedUID int) (Client, *Server) {
	t.Helper()
	temporary, err := os.CreateTemp("", "capture-socket-")
	if err != nil {
		t.Fatal(err)
	}
	path := temporary.Name()
	_ = temporary.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	server := NewServer(path, -1, allowedUID, engine)
	ctx, cancel := context.WithCancel(context.Background())
	var handlers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() { defer handlers.Done(); server.handle(ctx, connection) }()
		}
	}()
	t.Cleanup(func() { cancel(); _ = listener.Close(); <-done; handlers.Wait(); _ = os.Remove(path) })
	return NewClient(path), &server
}

func TestHelperCaptureDescriptorRetainsOriginalAndIsReadOnly(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	first, manifest, err := client.ReadAccountCapture(context.Background(), "read-capture", capture.Binding)
	if err != nil || first != capture || len(manifest.Entries) != 1 {
		t.Fatalf("manifest: %#v %v", first, err)
	}
	file, entry, err := client.OpenAccountCaptureObject(context.Background(), "read-object", capture, manifest.Entries[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("overwrite")); err == nil {
		t.Fatal("capture descriptor is writable")
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("capture descriptor can escape through exec")
	}
	content, err := io.ReadAll(file)
	if err != nil || skillmanager.VerifyContent(entry, content) != nil {
		t.Fatalf("content: %v", err)
	}
	if err := os.RemoveAll(engine.config.AccountRoot); err != nil {
		t.Fatal(err)
	}
	replay, repeated, err := client.ReadAccountCapture(context.Background(), "read-after-source-removal", capture.Binding)
	if err != nil || replay != capture || len(repeated.Entries) != 1 {
		t.Fatal("transfer reread original source", err)
	}
}

func TestHelperCaptureDescriptorRejectsChangedBindingAndUndeclaredObjects(t *testing.T) {
	for _, change := range []string{"node", "user", "account", "task", "takeover", "epoch", "backend", "inventory", "helper", "tree", "object"} {
		t.Run(change, func(t *testing.T) {
			engine, capture := captureTransferFixture(t)
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			_, manifest, err := client.ReadAccountCapture(context.Background(), "read-manifest", capture.Binding)
			if err != nil {
				t.Fatal(err)
			}
			digest := manifest.Entries[0].SHA256
			other := "77777777-7777-4777-8777-777777777777"
			switch change {
			case "node":
				capture.Binding.NodeID = other
			case "user":
				capture.Binding.UserID = other
			case "account":
				capture.Binding.AccountID = other
			case "task":
				capture.Binding.TaskID = other
			case "takeover":
				capture.Binding.TakeoverID = other
			case "epoch":
				capture.Binding.DirectoryEpoch++
			case "backend":
				capture.Binding.RuntimeBackend = "docker_sandbox"
			case "inventory":
				capture.Binding.InventoryDigest = strings.Repeat("a", 64)
			case "helper":
				capture.HelperReceiptID = other
			case "tree":
				capture.TreeDigest = strings.Repeat("a", 64)
			case "object":
				digest = strings.Repeat("a", 64)
			}
			file, _, err := client.OpenAccountCaptureObject(context.Background(), "denied-object", capture, digest)
			if err == nil || file != nil {
				if file != nil {
					_ = file.Close()
				}
				t.Fatal("unbound object transferred")
			}
			var failure *Error
			if !errors.As(err, &failure) || failure.Code != "CAPTURE_UNAVAILABLE" {
				t.Fatalf("unsafe failure: %v", err)
			}
		})
	}
}

func TestHelperCaptureDescriptorRejectsPrivateFileAliases(t *testing.T) {
	for _, kind := range []string{"manifest_link", "manifest_hardlink", "object_link", "object_hardlink", "object_mode", "damaged_manifest"} {
		t.Run(kind, func(t *testing.T) {
			engine, capture := captureTransferFixture(t)
			client, _ := serveCaptureTest(t, engine, os.Getuid())
			_, manifest, err := client.ReadAccountCapture(context.Background(), "before-damage", capture.Binding)
			if err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID)
			path := filepath.Join(bundle, "manifest.json")
			if strings.HasPrefix(kind, "object") {
				path = filepath.Join(bundle, "objects", manifest.Entries[0].SHA256)
			}
			switch kind {
			case "manifest_link", "object_link":
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".saved", path); err != nil {
					t.Fatal(err)
				}
			case "manifest_hardlink", "object_hardlink":
				if err := os.Link(path, path+".linked"); err != nil {
					t.Fatal(err)
				}
			case "object_mode":
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			case "damaged_manifest":
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(kind, "object") {
				file, _, err := client.OpenAccountCaptureObject(context.Background(), "after-damage", capture, manifest.Entries[0].SHA256)
				if err == nil || file != nil {
					if file != nil {
						_ = file.Close()
					}
					t.Fatal("unsafe object exported")
				}
			} else if _, _, err := client.ReadAccountCapture(context.Background(), "after-damage", capture.Binding); err == nil {
				t.Fatal("unsafe manifest exported")
			}
		})
	}
}

type captureChildInput struct {
	Socket  string
	Store   string
	Capture skillmanager.AccountCapture
	Denied  bool
}

func TestHelperCaptureDescriptorUnprivilegedReader(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_CAPTURE_FD_CHILD") == "1" {
		var input captureChildInput
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if os.Geteuid() == 0 {
			t.Fatal("reader is unexpectedly privileged")
		}
		if file, err := os.Open(input.Store); err == nil {
			_ = file.Close()
			t.Fatal("worker can traverse private store")
		}
		client := NewClient(input.Socket)
		capture, manifest, err := client.ReadAccountCapture(context.Background(), "unprivileged-manifest", input.Capture.Binding)
		if input.Denied {
			if err == nil {
				t.Fatal("unauthorized socket peer admitted")
			}
			return
		}
		if err != nil || capture != input.Capture {
			t.Fatalf("unprivileged manifest: %v", err)
		}
		file, entry, err := client.OpenAccountCaptureObject(context.Background(), "unprivileged-object", capture, manifest.Entries[0].SHA256)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, err := file.Write([]byte("altered")); err == nil {
			t.Fatal("worker mutated retained object")
		}
		content, err := io.ReadAll(file)
		if err != nil || skillmanager.VerifyContent(entry, content) != nil {
			t.Fatal("worker cannot verify descriptor stream", err)
		}
		return
	}
	engine, capture := captureTransferFixture(t)
	client, _ := serveCaptureTest(t, engine, 65534)
	for _, parent := range []string{filepath.Dir(engine.config.SkillStateRoot), filepath.Dir(filepath.Dir(engine.config.SkillStateRoot))} {
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []uint32{65534, 65533} {
		input, _ := json.Marshal(captureChildInput{Socket: client.socketPath, Store: engine.config.SkillStateRoot, Capture: capture, Denied: uid != 65534})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestHelperCaptureDescriptorUnprivilegedReader$", "-test.v")
		command.Env = append(os.Environ(), "AGENT_REMOTE_CAPTURE_FD_CHILD=1")
		command.Stdin = bytes.NewReader(input)
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid}}
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("UID %d: %v\n%s", uid, err, output)
		}
	}
}

func TestHelperCaptureDescriptorNeverCreatesMissingStoreOrAcceptsPaths(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	missing := filepath.Join(filepath.Dir(engine.config.SkillStateRoot), "absent", "private-store")
	engine.config.SkillStateRoot = missing
	requestPayload := accountCaptureFileRequest{Binding: capture.Binding, Kind: "manifest"}
	mapped, _ := Map(requestPayload)
	request := Request{Version: 1, RequestID: "readonly-query", Operation: accountCaptureFileOperation, Payload: mapped}
	for _, change := range []string{"absent", "path", "version", "operation", "request_id", "kind", "object_digest"} {
		t.Run(change, func(t *testing.T) {
			candidate := request
			candidate.Payload, _ = Map(requestPayload)
			switch change {
			case "path":
				candidate.Payload["path"] = "/etc/shadow"
			case "version":
				candidate.Version++
			case "operation":
				candidate.Operation = "read_file"
			case "request_id":
				candidate.RequestID = "../unsafe"
			case "kind":
				candidate.Payload["kind"] = "directory"
			case "object_digest":
				candidate.Payload["digest"] = strings.Repeat("a", 64)
			}
			file, _, err := engine.openAccountCaptureFile(context.Background(), candidate)
			if err == nil || file != nil {
				if file != nil {
					_ = file.Close()
				}
				t.Fatal("invalid read operation accepted")
			}
			if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
				t.Fatal("read operation created store ancestors", err)
			}
		})
	}
}
