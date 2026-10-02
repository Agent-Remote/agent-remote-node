"""Linux privilege/TTY integration; synthetic tool only, no user account or clipboard."""
import array
import fcntl
import json
import os
import pathlib
import pty
import select
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import time


def main(binary):
    with tempfile.TemporaryDirectory(prefix="terminal-") as directory:
        root = pathlib.Path(directory)
        root.chmod(0o755)
        tmuxdir = root / "tmux"
        tmuxdir.mkdir(mode=0o700)
        os.chown(tmuxdir, 65534, 65534)
        bridge = str(root / "bridge.sock")
        tmuxsocket = str(tmuxdir / "tmux.sock")
        stale = socket.socket(socket.AF_UNIX)
        stale.bind(bridge)
        stale.close()
        receipt = root / "receipt.json"
        tool = root / "tool.py"
        tool.write_text('''import json, os, signal, sys, tty
assert os.geteuid() == 0
assert all(os.isatty(fd) for fd in (0, 1, 2))
tty.setraw(0)
signal.signal(signal.SIGWINCH, lambda *_: print("RESIZED", flush=True))
print("\\033[>4;2m\\033[?2004h\\033[?1000h\\033[?1006hcopy this response\\r\\nTOOL_READY", flush=True)
data = b""
while b"DONE" not in data:
    data += os.read(0, 4096)
    if data.endswith(bytes([3, 27])):
        print("PASTE_READ", flush=True)
with open(sys.argv[1], "w") as output:
    json.dump({"input": data.decode(), "size": list(os.get_terminal_size(0))}, output)
''')
        launch = dict(TmuxBinary="/usr/bin/tmux", RuntimeBinary=binary,
                      Socket=tmuxsocket, Bridge=bridge, Session="managed",
                      UID=65534, GID=65534,
                      Command=["/usr/bin/python3", str(tool), str(receipt)],
                      Environment=["PATH=/usr/bin:/bin", "LANG=C.UTF-8"])
        host = subprocess.Popen([binary, "terminal-host"], stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        host.stdin.write(json.dumps(launch).encode() + b"\n")
        host.stdin.close()
        master = slave = None
        client = None
        try:
            assert select.select([host.stdout], [], [], 10)[0], "host startup timed out"
            assert host.stdout.readline() == b"ready\n", host.stderr.read().decode()
            server_pid = int(subprocess.check_output(["tmux", "-S", tmuxsocket,
                "display-message", "-p", "#{pid}"]))
            status = pathlib.Path(f"/proc/{server_pid}/status").read_text()
            assert "Uid:\t65534\t65534\t65534\t65534" in status, status
            assert os.stat(bridge).st_mode & 0o777 == 0o600

            # A root peer is not the configured terminal peer and cannot launch it.
            rejected = socket.socket(socket.AF_UNIX)
            rejected.connect(bridge)
            rejected.settimeout(2)
            assert rejected.recv(1) == b""
            rejected.close()
            # Even the authorized UID cannot substitute a non-terminal file.
            pid = os.fork()
            if pid == 0:
                os.setgroups([])
                os.setgid(65534)
                os.setuid(65534)
                invalid = socket.socket(socket.AF_UNIX)
                invalid.connect(bridge)
                descriptor = os.open("/dev/null", os.O_RDWR)
                invalid.sendmsg([b"T"], [(socket.SOL_SOCKET, socket.SCM_RIGHTS,
                                         array.array("i", [descriptor]))])
                invalid.settimeout(2)
                assert invalid.recv(1) == b""
                os._exit(0)
            assert os.waitpid(pid, 0)[1] == 0

            # Model a terminal that advertises extended-key support; applications
            # must still request the protocol before tmux forwards extended keys.
            subprocess.check_call(["tmux", "-S", tmuxsocket, "set-option", "-s",
                                   "terminal-features[101]", "xterm*:extkeys"])
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
            client = subprocess.Popen(["tmux", "-S", tmuxsocket, "attach-session", "-t", "managed"],
                                      stdin=slave, stdout=slave, stderr=slave,
                                      env=dict(os.environ, TERM="xterm-256color"))
            os.close(slave)
            slave = None
            output = bytearray()

            def until(marker):
                deadline = time.monotonic() + 10
                while marker not in output:
                    assert time.monotonic() < deadline, ("missing output", marker, bytes(output))
                    if select.select([master], [], [], 0.1)[0]:
                        output.extend(os.read(master, 65536))

            until(b"TOOL_READY")
            os.write(master, b"\x1b[<0;1;1M\x1b[<32;5;1M\x1b[<0;5;1m")
            until(b"\x1b]52;")
            os.write(master, b"\x02]")
            os.write(master, b"\x1b[27;2;13~\x1ba")
            # Paste bytes, UTF-8 and controls retain their exact application meaning.
            pasted = "\x1b[200~第一行\n  second line\x1b[201~\x03\x1b".encode()
            os.write(master, pasted)
            until(b"PASTE_READ")
            # A second client takes over the same running pane. Detaching it
            # leaves both tmux and the privileged fixed command alive.
            pane_pid = subprocess.check_output(["tmux", "-S", tmuxsocket, "display-message", "-p", "#{pane_pid}"])
            for detach in (False, True):
                previous_client, previous_master = client, master
                if detach:
                    os.write(previous_master, b"\x02d")
                    assert previous_client.wait(timeout=5) == 0
                    assert host.poll() is None
                master, slave = pty.openpty()
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
                client = subprocess.Popen(["tmux", "-S", tmuxsocket, "attach-session", "-d", "-f", "!ignore-size", "-t", "managed"],
                    stdin=slave, stdout=slave, stderr=slave, env=dict(os.environ, TERM="xterm-256color"))
                os.close(slave)
                slave = None
                assert previous_client.wait(timeout=5) == 0
                os.close(previous_master)
                output.clear()
                until(b"TOOL_READY")
                assert subprocess.check_output(["tmux", "-S", tmuxsocket, "display-message", "-p", "#{pane_pid}"]) == pane_pid
            fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 110, 0, 0))
            client.send_signal(signal.SIGWINCH)
            until(b"RESIZED")
            # Batched prefixes must not be misclassified as a paste.
            os.write(master, b"\x02:\x02c")
            os.write(master, b"DONE")
            assert host.wait(timeout=10) == 0, host.stderr.read().decode()
            result = json.loads(receipt.read_text())
            assert pasted.decode() in result["input"], result
            assert "copy" in result["input"], result
            assert any(key in result["input"] for key in ("\x1b[27;2;13~", "\x1b[13;2u")), result
            assert any(key in result["input"] for key in ("\x1ba", "\x1b[27;3;97~", "\x1b[97;3u")), result
            assert result["size"] == [110, 32], result
            assert "\x02" not in result["input"] and ":" not in result["input"], result
            assert not pathlib.Path(f"/proc/{server_pid}").exists() or "State:\tZ" in pathlib.Path(f"/proc/{server_pid}/status").read_text()
            print("PASS: non-root tmux, peer/descriptor rejection, mouse copy, paste, takeover, detach/reconnect, resize and cleanup")
        finally:
            if host.poll() is None:
                host.terminate()
                host.wait(timeout=10)
            if client and client.poll() is None:
                client.terminate()
                client.wait(timeout=5)
            for fd in (master, slave):
                if fd is not None:
                    os.close(fd)


if __name__ == "__main__":
    main(sys.argv[1])
