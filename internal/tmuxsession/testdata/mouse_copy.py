"""Real tmux mouse selection through a PTY, with an app requesting mouse events."""
import base64
import fcntl
import os
import pty
import re
import select
import struct
import subprocess
import sys
import termios
import time

binary, socket, session = sys.argv[1:]
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
env = dict(os.environ, TERM="xterm-256color")
env.pop("TMUX", None)
client = subprocess.Popen([binary, "-S", socket, "attach-session", "-t", session],
                          stdin=slave, stdout=slave, stderr=slave, env=env)
os.close(slave)
output = bytearray()

def receive_until(predicate):
    deadline = time.monotonic() + 5
    while not predicate(output):
        assert time.monotonic() < deadline, "tmux output timed out"
        if select.select([master], [], [], .1)[0]:
            output.extend(os.read(master, 65536))

try:
    receive_until(lambda data: b"copy this response" in data)
    # Button down, drag, release. No keyboard copy shortcut and no clipboard tool.
    # One burst reproduces SSH batching and tmux's PANE_REDRAW clipboard race.
    os.write(master, b"\x1b[<0;1;1M\x1b[<32;19;1M\x1b[<0;19;1m")
    pattern = rb"\x1b\]52;[^;]*;([A-Za-z0-9+/=]+)(?:\x07|\x1b\\)"
    receive_until(lambda data: re.search(pattern, data))
    copied = base64.b64decode(re.search(pattern, output).group(1))
    assert copied.rstrip() == b"copy this response", "drag did not copy the selected response"
    # Repeating the same explicit selection must copy again, even while the
    # short tmux acknowledgement is visible.
    for count in (2, 3):
        os.write(master, b"\x1b[<0;1;1M\x1b[<32;19;1M\x1b[<0;19;1m")
        receive_until(lambda data: len(re.findall(pattern, data)) >= count)
    assert all(base64.b64decode(value).rstrip() == b"copy this response"
               for value in re.findall(pattern, output)), "repeated selection changed text"
    mode = subprocess.check_output([binary, "-S", socket, "display-message", "-p", "-t", session, "#{pane_in_mode}"])
    assert mode.strip() == b"0", "mouse release did not leave copy mode"
except Exception:
    print("terminal fixture output:", repr(bytes(output)))
    for args in [["show-buffer"], ["display-message", "-p", "#{pane_in_mode} #{mouse_any_flag} #{client_termname}"], ["info"]]:
        result = subprocess.run([binary, "-S", socket, *args], capture_output=True)
        if args == ["info"]:
            print([line for line in result.stdout.splitlines() if b"Ms:" in line])
        else:
            print(args, repr(result.stdout), repr(result.stderr))
    raise
finally:
    client.terminate()
    client.wait(timeout=5)
    os.close(master)
