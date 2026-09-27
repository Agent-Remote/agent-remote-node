package runtimehelper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
)

func fakeCapturePeer(t *testing.T, send func(*net.UnixConn)) (Client, func()) {
	t.Helper()
	file, err := os.CreateTemp("", "capture-peer-")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		var request Request
		if json.NewDecoder(connection).Decode(&request) != nil {
			return
		}
		send(connection)
	}()
	cleanup := func() { _ = listener.Close(); <-done; _ = os.Remove(path) }
	return NewClient(path), cleanup
}

func countCaptureDescriptors(t *testing.T, path string) int {
	t.Helper()
	files, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, file := range files {
		target, _ := os.Readlink(filepath.Join("/proc/self/fd", file.Name()))
		if target == path {
			count++
		}
	}
	return count
}

func TestHelperCaptureDescriptorRejectsMalformedFramesWithoutLeaking(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	manifestPath := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID, "manifest.json")
	for _, kind := range []string{"missing_fd", "two_fds", "truncated_rights", "incomplete", "oversized", "duplicate_key", "wrong_version", "wrong_binding", "wrong_size", "writable", "pipe", "trailing_json"} {
		t.Run(kind, func(t *testing.T) {
			file, err := os.Open(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			info, _ := file.Stat()
			metadata := accountCaptureFileResponse{Capture: capture, Kind: "manifest", Size: info.Size()}
			if kind == "wrong_binding" {
				metadata.Capture.Binding.DirectoryEpoch++
			}
			if kind == "wrong_size" {
				metadata.Size++
			}
			if kind == "writable" {
				_ = file.Close()
				file, err = os.OpenFile(manifestPath, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
			}
			if kind == "pipe" {
				_ = file.Close()
				read, write, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				file = read
				defer read.Close()
				defer write.Close()
			}
			result, _ := Map(metadata)
			response := Response{Version: 1, OK: true, Result: result}
			if kind == "wrong_version" {
				response.Version++
			}
			encoded, _ := json.Marshal(response)
			data := string(encoded) + "\n"
			switch kind {
			case "incomplete":
				data = data[:len(data)/2]
			case "oversized":
				data = strings.Repeat("x", maxCaptureFileResponseBytes+1) + "\n"
			case "duplicate_key":
				data = strings.Replace(data, `"version":1`, `"version":1,"version":1`, 1)
			case "trailing_json":
				data += "{}"
			}
			descriptors := []int{int(file.Fd())}
			if kind == "missing_fd" {
				descriptors = nil
			}
			if kind == "two_fds" {
				descriptors = append(descriptors, int(file.Fd()))
			}
			if kind == "truncated_rights" {
				descriptors = make([]int, 32)
				for i := range descriptors {
					descriptors[i] = int(file.Fd())
				}
			}
			before := countCaptureDescriptors(t, manifestPath)
			client, stop := fakeCapturePeer(t, func(connection *net.UnixConn) {
				var rights []byte
				if len(descriptors) > 0 {
					rights = syscall.UnixRights(descriptors...)
				}
				_, _, _ = connection.WriteMsgUnix([]byte(data), rights, nil)
			})
			_, _, err = client.ReadAccountCapture(context.Background(), "malformed-response", capture.Binding)
			stop()
			if err == nil {
				t.Fatal("malformed or unsafe transfer accepted")
			}
			if after := countCaptureDescriptors(t, manifestPath); after != before {
				t.Fatalf("descriptor leak: before %d, after %d", before, after)
			}
		})
	}
}

func TestHelperCaptureDescriptorAcceptsFragmentedMetadata(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	path := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID, "manifest.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	result, _ := Map(accountCaptureFileResponse{Capture: capture, Kind: "manifest", Size: info.Size()})
	data, _ := json.Marshal(Response{Version: 1, OK: true, Result: result})
	data = append(data, '\n')
	client, stop := fakeCapturePeer(t, func(connection *net.UnixConn) {
		_, _ = connection.Write(data[:17])
		_, _, _ = connection.WriteMsgUnix(data[17:40], syscall.UnixRights(int(file.Fd())), nil)
		_, _ = connection.Write(data[40:])
		var ack [1]byte
		if _, err := io.ReadFull(connection, ack[:]); err != nil || ack[0] != fileDescriptorTransferAck {
			t.Error("descriptor acknowledgement missing", err)
		}
	})
	receipt, manifest, err := client.ReadAccountCapture(context.Background(), "fragmented-response", capture.Binding)
	stop()
	if err != nil || receipt != capture || len(manifest.Entries) != 1 {
		t.Fatal("fragmented response failed", err)
	}
}

func TestHelperCaptureDescriptorCancellationClosesPartialTransfer(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	path := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID, "manifest.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	baseline := countCaptureDescriptors(t, path)
	sent := make(chan struct{})
	client, stop := fakeCapturePeer(t, func(connection *net.UnixConn) {
		_, _, _ = connection.WriteMsgUnix([]byte(`{"version":1`), syscall.UnixRights(int(file.Fd())), nil)
		close(sent)
		var buffer [1]byte
		_, _ = connection.Read(buffer[:])
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-sent; cancel() }()
	started := time.Now()
	_, _, err = client.ReadAccountCapture(ctx, "cancelled-read", capture.Binding)
	stop()
	if err != context.Canceled || time.Since(started) > time.Second {
		t.Fatalf("cancellation did not interrupt descriptor read: %v", err)
	}
	if countCaptureDescriptors(t, path) != baseline {
		t.Fatal("cancelled descriptor leaked")
	}
}

func TestHelperCaptureDescriptorManifestExceedsOrdinaryFrameLimit(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	manifest := skillmanager.Manifest{Version: 1, Entries: make([]skillmanager.Entry, 10000)}
	for index := range manifest.Entries {
		manifest.Entries[index] = skillmanager.Entry{Path: fmt.Sprintf("directory-%05d", index), Kind: "directory", Mode: 0o700}
	}
	var err error
	capture.TreeDigest, err = skillmanager.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil || len(data) <= maxHelperResponseBytes {
		t.Fatal("manifest does not exercise descriptor transport", len(data), err)
	}
	bundle := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID)
	record, _ := json.Marshal(capture)
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "record.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	client, _ := serveCaptureTest(t, engine, os.Getuid())
	received, decoded, err := client.ReadAccountCapture(context.Background(), "large-manifest", capture.Binding)
	if err != nil || received != capture || len(decoded.Entries) != 10000 {
		t.Fatal("large manifest transfer failed", err)
	}
}

func TestHelperCaptureDescriptorSupportsEscapedPortablePaths(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	helper, _ := serveCaptureTest(t, engine, os.Getuid())
	_, manifest, err := helper.ReadAccountCapture(context.Background(), "before-long-path", capture.Binding)
	if err != nil {
		t.Fatal(err)
	}
	fileEntry := manifest.Entries[0]
	manifest.Entries = nil
	parent := ""
	for range 15 {
		if parent != "" {
			parent += "/"
		}
		parent += strings.Repeat("<", 220)
		manifest.Entries = append(manifest.Entries, skillmanager.Entry{Path: parent, Kind: "directory", Mode: 0o700})
	}
	fileEntry.Path = parent + "/state"
	manifest.Entries = append(manifest.Entries, fileEntry)
	capture.TreeDigest, err = skillmanager.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID)
	data, _ := json.Marshal(manifest)
	record, _ := json.Marshal(capture)
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "record.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(accountCaptureFileResponse{Capture: capture, Kind: "object", Size: fileEntry.Size, Entry: &fileEntry})
	if len(metadata) <= 16<<10 {
		t.Fatal("fixture does not exercise JSON path expansion")
	}
	file, entry, err := helper.OpenAccountCaptureObject(context.Background(), "escaped-object-path", capture, fileEntry.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if entry.Path != fileEntry.Path {
		t.Fatal("portable path was changed during descriptor transfer")
	}
	content, err := io.ReadAll(file)
	if err != nil || skillmanager.VerifyContent(entry, content) != nil {
		t.Fatal("long path object failed", err)
	}
}

func TestHelperCaptureDescriptorPreservesFullIntegerEpoch(t *testing.T) {
	engine, capture := captureTransferFixture(t)
	path := filepath.Join(engine.config.SkillStateRoot, "takeover-"+capture.Binding.AccountID, "manifest.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	capture.Binding.DirectoryEpoch = 9007199254740993
	result, _ := Map(accountCaptureFileResponse{Capture: capture, Kind: "manifest", Size: info.Size()})
	data, _ := json.Marshal(Response{Version: 1, OK: true, Result: result})
	data = append(data, '\n')
	for _, exact := range []bool{true, false} {
		expected := capture.Binding
		if !exact {
			expected.DirectoryEpoch--
		}
		client, stop := fakeCapturePeer(t, func(connection *net.UnixConn) {
			_, _, _ = connection.WriteMsgUnix(data, syscall.UnixRights(int(file.Fd())), nil)
			var ack [1]byte
			_, _ = connection.Read(ack[:])
		})
		received, _, err := client.ReadAccountCapture(context.Background(), "full-integer-epoch", expected)
		stop()
		if exact && (err != nil || received != capture) {
			t.Fatalf("valid full-width epoch was lost: %v", err)
		}
		if !exact && err == nil {
			t.Fatal("rounded epoch impersonated another binding")
		}
	}
}
