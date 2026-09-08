package egobrowser

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	linuxPeerACLTestEnvironment   = "AGENT_REMOTE_RUN_LINUX_UID_ACL_TEST"
	linuxPeerACLHelperEnvironment = "AGENT_REMOTE_LINUX_UID_ACL_HELPER"
	linuxPeerSocketEnvironment    = "AGENT_REMOTE_LINUX_UID_ACL_SOCKET"
	linuxPeerConnectEnvironment   = "AGENT_REMOTE_LINUX_UID_ACL_EXPECT_CONNECT"
	linuxPeerAuthEnvironment      = "AGENT_REMOTE_LINUX_UID_ACL_EXPECT_AUTHORIZED"
)

func TestLinuxNativeRuntimePeerUIDACLIntegration(t *testing.T) {
	if os.Getenv(linuxPeerACLTestEnvironment) != "1" {
		t.Skip("set AGENT_REMOTE_RUN_LINUX_UID_ACL_TEST=1 in a rootful Linux environment")
	}
	if os.Geteuid() != 0 {
		t.Fatal("Linux UID/ACL integration requires root to launch exact peer UIDs")
	}
	for _, binary := range []string{"setfacl", "getfacl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("required ACL utility %s is unavailable: %v", binary, err)
		}
	}

	socketRoot, err := os.MkdirTemp("/tmp", "agent-remote-ego-browser-acl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	if err := os.Chmod(socketRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	broker, err := New(Config{
		Enabled: true, NodeID: "node-linux-acl", StateRoot: stateRoot,
		SocketPath: filepath.Join(socketRoot, "broker.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	nonce, _, err := broker.RegisterToolSession("session-linux-acl")
	if err != nil {
		t.Fatal(err)
	}

	const authorizedUID uint32 = 20001
	const rejectedUID uint32 = 20002
	if err := broker.AuthorizeToolSessionPeer("session-linux-acl", nonce, authorizedUID); err != nil {
		t.Fatal(err)
	}
	assertExactAccessACL(t, socketRoot, []string{
		"user::rwx", fmt.Sprintf("user:%d:--x", authorizedUID), "group::---", "mask::--x", "other::---",
	})
	assertExactAccessACL(t, broker.SocketPath(), []string{
		"user::rw-", fmt.Sprintf("user:%d:rw-", authorizedUID), "group::---", "mask::rw-", "other::---",
	})

	helperPath := copyLinuxPeerHelper(t)
	runLinuxPeerHelper(t, broker, nonce, helperPath, authorizedUID, true, true)
	runLinuxPeerHelper(t, broker, nonce, helperPath, rejectedUID, false, false)

	setAccessACL(t, socketRoot, fmt.Sprintf("u:%d:--x", rejectedUID))
	setAccessACL(t, broker.SocketPath(), fmt.Sprintf("u:%d:rw-", rejectedUID))
	runLinuxPeerHelper(t, broker, nonce, helperPath, rejectedUID, true, false)
}

func TestLinuxNativeRuntimePeerUIDACLHelper(t *testing.T) {
	if os.Getenv(linuxPeerACLHelperEnvironment) != "1" {
		t.Skip("integration helper process")
	}
	wantConnect := os.Getenv(linuxPeerConnectEnvironment) == "1"
	connection, err := net.DialTimeout("unix", os.Getenv(linuxPeerSocketEnvironment), 3*time.Second)
	if !wantConnect {
		if err == nil {
			_ = connection.Close()
			t.Fatal("peer without an ACL unexpectedly connected")
		}
		return
	}
	if err != nil {
		t.Fatalf("peer could not connect: %v", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	response := []byte{0}
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	wantAuthorized := os.Getenv(linuxPeerAuthEnvironment) == "1"
	if (response[0] == 1) != wantAuthorized {
		t.Fatalf("unexpected peer authorization result: got=%d want_authorized=%v", response[0], wantAuthorized)
	}
}

func runLinuxPeerHelper(
	t *testing.T,
	broker *Broker,
	nonce string,
	helperPath string,
	uid uint32,
	wantConnect bool,
	wantAuthorized bool,
) {
	t.Helper()
	command := exec.Command(helperPath, "-test.run=^TestLinuxNativeRuntimePeerUIDACLHelper$")
	command.Dir = "/tmp"
	command.Env = append(os.Environ(),
		linuxPeerACLHelperEnvironment+"=1",
		linuxPeerSocketEnvironment+"="+broker.SocketPath(),
		linuxPeerConnectEnvironment+"="+boolEnvironment(wantConnect),
		linuxPeerAuthEnvironment+"="+boolEnvironment(wantAuthorized),
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: uid},
		Pdeathsig:  syscall.SIGKILL,
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if wantConnect {
		listener := broker.listener
		if listener == nil {
			t.Fatal("broker listener was not initialized")
		}
		if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		connection, err := listener.AcceptUnix()
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("accept peer uid %d: %v", uid, err)
		}
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		signal := []byte{0}
		if _, err := io.ReadFull(connection, signal); err != nil {
			_ = connection.Close()
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("read peer uid %d readiness: %v", uid, err)
		}
		authorizeErr := broker.authorizePeer(connection, nonce)
		status := byte(0)
		if authorizeErr == nil {
			status = 1
		}
		_, writeErr := connection.Write([]byte{status})
		_ = connection.Close()
		if writeErr != nil {
			t.Fatalf("write peer uid %d result: %v", uid, writeErr)
		}
		if wantAuthorized && authorizeErr != nil {
			t.Fatalf("authorized peer uid %d was rejected: %v", uid, authorizeErr)
		}
		if !wantAuthorized && !errors.Is(authorizeErr, ErrProtocol) {
			t.Fatalf("unexpected peer uid %d authorization result: %v", uid, authorizeErr)
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("peer uid %d helper failed: %v\n%s", uid, err, output.String())
	}
}

func assertExactAccessACL(t *testing.T, path string, expected []string) {
	t.Helper()
	output, err := exec.Command("getfacl", "--absolute-names", "--numeric", "--omit-header", path).CombinedOutput()
	if err != nil {
		t.Fatalf("getfacl %s: %v: %s", path, err, output)
	}
	actual := make([]string, 0, len(expected))
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			actual = append(actual, line)
		}
	}
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected ACL for %s:\n%s\nwant:\n%s", path, strings.Join(actual, "\n"), strings.Join(expected, "\n"))
	}
}

func setAccessACL(t *testing.T, path string, access string) {
	t.Helper()
	if output, err := exec.Command("setfacl", "-m", access, path).CombinedOutput(); err != nil {
		t.Fatalf("setfacl %s %s: %v: %s", access, path, err, output)
	}
}

func copyLinuxPeerHelper(t *testing.T) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.CreateTemp("/tmp", "agent-remote-ego-browser-peer-")
	if err != nil {
		t.Fatal(err)
	}
	path := destination.Name()
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func boolEnvironment(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
