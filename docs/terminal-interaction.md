# Managed terminal interaction

The supported flow is desktop terminal → local CLI/OpenSSH → authorized SSH gateway → private
non-root tmux → Claude. Login detection and desktop clipboard writes live in the local CLI;
tmux emits OSC 52 only for user selections. Ordinary input stays on OpenSSH's real stdin TTY.

## Runtime and upgrade

Native keeps its existing non-root systemd/Bubblewrap boundary and per-session socket. Docker
uses a separate transient `agent-remote-terminal-<digest>.service`: the root host holds one fixed
Docker exec, while tmux runs under the per-user runtime UID without Docker permissions. The
pane client hands its TTY to that host via SCM_RIGHTS. SO_PEERCRED, TTY validation and comparison
with the designated pane prevent a client from supplying a command or an unrelated descriptor.
Only resize notifications are accepted afterwards. The launch manifest (including any ephemeral
environment) travels over private stdin, never a file, tmux environment or systemd argument.
The tmux socket and bridge are outside the sandbox mounts. Stop first ends the unit, then removes
the Docker sandbox. The terminal service requires systemd 249+, useradd and normal POSIX ACL tools.

Existing Docker specs remain readable for observation and cleanup. Old privileged/default-socket
terminals fail closed on attach and must be stopped and recreated; account login must be restarted.
Do not leave previously attached legacy Docker terminals running during rollout. There is no
automatic command replay, privilege migration or termination of users' sessions during upgrade.
New history limits apply only to newly created panes. Native existing sessions receive key policy
when reattached; use new sessions when validating the full fresh-server configuration.

## Interaction policy

- Drag and release selects/copies; a short tmux message acknowledges the send request.
- Ctrl+B then D detaches; connection loss retains the session. A new client takes over the display.
- Ctrl+B then [ opens history. Arrows/PageUp/PageDown navigate, Space selects, Enter/Y copies,
  Esc/Q cancels. Vi copy mode also supports h/j/k/l, g/G and v.
- Ctrl+B then ] pastes the tmux buffer with bracketed-paste support; Ctrl+B twice sends Ctrl+B.
- Host command prompts, menus, new windows, splits, session selectors and copy-pipe are unavailable.
- SSH batching is not treated as an implicit paste (`assume-paste-time=0`); explicit bracketed
  paste remains supported. History wheel scrolling advances five lines per event.
- New panes retain 20,000 lines. RGB is an additional capability entry. Extended keys are enabled
  only by negotiation and application request. Esc timeout stays at 10 ms. Unsupported terminals
  retain ordinary key handling; this does not guarantee Shift+Enter in every terminal.
- Clipboard writes are UTF-8, limited to 64 KiB. The local CLI rings a bell on rejected payloads
  and prints details after the TUI exits. OSC 52 fallback cannot confirm desktop clipboard success.

## Automated validation

`go test ./internal/tmuxsession` runs isolated real tmux servers to check mouse-release OSC 52,
restricted reachable key tables, retained default terminal capabilities, actual pane history size,
startup geometry and resize behavior. `tests/linux_terminal_host_test.sh` builds the actual runtime
binary and runs it as root in a disposable Linux container with real tmux and PTYs. It verifies the
tmux process UID, private socket modes, rejection of the wrong peer UID and non-TTY descriptors,
drag copying, UTF-8/bracketed multiline paste, negotiated Shift+Enter and Alt keys, Ctrl+C/Esc bytes, takeover, detach/reconnect, window resize propagation and cleanup.
The fixture uses a synthetic tool, never a user's Claude account or desktop clipboard. CI runs it
alongside the normal Go, installer and coverage gates. The CLI separately tests slow-copy EOF races,
latest-pending-selection handling, rejected selections, bounded shutdown, sync failure/timeout, and
PTY login link/selection transport.

## Desktop qualification still required

Before release, exercise Terminal.app, iTerm2, Warp, VS Code, Windows Terminal (native and WSL), and
a Linux Wayland/X11 terminal over real OpenSSH. For each, verify first login/relogin link copying,
fast repeated drags, long scrolling selections, 64 KiB overflow feedback, IME input, Shift+Enter and
Alt combinations, multiline paste, Ctrl+C/Esc, reconnect/takeover, resize and local nested tmux.
Check both native clipboard tools and an OSC 52 fallback with clipboard permission denied.
Run the Docker lifecycle on a systemd host with the actual Docker Sandbox backend as well as Native.
Container/PTY results are not evidence that every desktop terminal or an actual Claude build has
been qualified. Image paste and file-drop transfer are outside this text-clipboard bridge.
