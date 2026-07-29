//go:build linux

package runtimehelper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestDialNetworkNamespaceLoopbackIntegration(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_NETNS_TEST") != "1" {
		t.Skip("set AGENT_REMOTE_RUN_NETNS_TEST=1 to run the root network namespace test")
	}
	if os.Geteuid() != 0 {
		t.Fatal("network namespace integration test must run as root")
	}
	namespace := fmt.Sprintf("ar-test-%d", os.Getpid())
	runNetnsCommand(t, "add", namespace)
	defer exec.Command("ip", "netns", "delete", namespace).Run()
	runNetnsCommand(t, "exec", namespace, "ip", "link", "set", "lo", "up")
	for _, arguments := range namespaceFirewallCommands(SessionSpec{}) {
		runNetnsCommand(t, append([]string{"exec", namespace, "nft"}, arguments...)...)
	}

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(t.TempDir(), "ready")
	helper := exec.Command(
		"ip", "netns", "exec", namespace, executable,
		"-test.run=^TestNetworkNamespaceEchoHelper$",
	)
	helper.Env = append(os.Environ(),
		"AGENT_REMOTE_NETNS_ECHO_HELPER=1",
		"AGENT_REMOTE_NETNS_ECHO_PORT="+strconv.Itoa(port),
		"AGENT_REMOTE_NETNS_ECHO_READY="+readyPath,
	)
	helperOutput := &lockedBuffer{}
	helper.Stdout = helperOutput
	helper.Stderr = helperOutput
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer helper.Process.Kill()
	waitForPath(t, readyPath, helper, helperOutput)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := dialNetworkNamespaceLoopback(ctx, namespace, port)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("namespace-echo")); err != nil {
		t.Fatal(err)
	}
	if err := connection.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(connection)
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "namespace-echo" {
		t.Fatalf("unexpected namespace response %q", payload)
	}
	if err := helper.Wait(); err != nil {
		t.Fatalf("namespace echo helper failed: %v: %s", err, helperOutput.String())
	}
}

func TestNetworkNamespaceEchoHelper(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_NETNS_ECHO_HELPER") != "1" {
		return
	}
	port, err := strconv.Atoi(os.Getenv("AGENT_REMOTE_NETNS_ECHO_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.WriteFile(os.Getenv("AGENT_REMOTE_NETNS_ECHO_READY"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.Copy(connection, connection); err != nil {
		t.Fatal(err)
	}
}

func runNetnsCommand(t *testing.T, arguments ...string) {
	t.Helper()
	if output, err := exec.Command("ip", append([]string{"netns"}, arguments...)...).CombinedOutput(); err != nil {
		t.Fatalf("ip netns %v failed: %v: %s", arguments, err, output)
	}
}

func waitForPath(t *testing.T, path string, helper *exec.Cmd, output *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if helper.ProcessState != nil && helper.ProcessState.Exited() {
			t.Fatalf("namespace echo helper exited early: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("namespace echo helper did not become ready: %s", output.String())
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(payload)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
