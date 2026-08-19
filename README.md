# agent-remote-node

<p align="center"><img src="assets/agent-remote-icon.svg" alt="Agent Remote icon" width="80" height="80"></p>

<p align="center">
  <a href="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Agent-Remote/agent-remote-node/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://codecov.io/gh/Agent-Remote/agent-remote-node"><img alt="Codecov" src="https://codecov.io/gh/Agent-Remote/agent-remote-node/graph/badge.svg"></a>
  <a href="https://github.com/Agent-Remote/agent-remote-node/stargazers"><img alt="GitHub Stars" src="https://img.shields.io/github/stars/Agent-Remote/agent-remote-node?style=flat&logo=github"></a>
  <img alt="Go 1.26.6" src="https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white">
  <a href="LICENSE"><img alt="License: GPL-3.0" src="https://img.shields.io/github/license/Agent-Remote/agent-remote-node"></a>
</p>

English | [中文](README.zh-CN.md)

Node-side runtime for agent-remote.

The node runs on a VPS and talks to `agent-remote-server` by polling the control plane. It does not expose public HTTP ports.

## Commands

```sh
go test ./...
```

```sh
go run ./cmd/agent-remote-node --help
```

```sh
go run ./cmd/agent-remote-node register \
  --config ./config.json \
  --server-url http://localhost:8000 \
  --node-id <node-id> \
  --registration-token <registration-token>
```

```sh
go run ./cmd/agent-remote-node heartbeat --config ./config.json
```

```sh
go run ./cmd/agent-remote-node poll-once --config ./config.json
```

```sh
go run ./cmd/agent-remote-node run --config ./config.json
```

```sh
go run ./cmd/agent-remote-node install-ssh --config ./config.json
```

```sh
go run ./cmd/agent-remote-attach --config ./config.json --session <session-id> --device <device-id> --dry-run
go run ./cmd/agent-remote-attach --config ./config.json --binding <tool-account-id> --device <device-id> --dry-run
```

`install-ssh` prepares the managed `authorized_keys` file. Runtime SSH keys are written atomically by the `sync_ssh_keys` node task with forced-command restrictions. Each task replaces only the named device's managed keys, preserving other devices while removing stale keys after rotation.

`prepare_workspace` tasks install the device's stable SSH gateway key and ask the privileged runtime helper to create the workspace as the control-plane user's Linux UID. Mutagen commands are re-authorized by device and node, then run without network access in a Bubblewrap view containing only that user's data.

`create_binding_session` and `create_tool_session` use the backend pinned by the control plane. Docker Sandbox remains supported. Native sessions run the managed Claude binary under a per-user UID with systemd cgroup limits, Bubblewrap filesystem isolation, a dedicated network namespace, nftables egress filtering, a quota-limited temporary filesystem, and a per-session tmux socket. Docker and browser operations also pass through the root helper; the node worker is not a member of the Docker group.

Native developer credential profiles provide persistent Git and GitHub CLI configuration directories. SSH private keys remain on the client: only an authorized `ssh -A` attach is bridged through a session-local Unix socket, and only while that connection is active. The gateway continues to deny TCP forwarding, X11 forwarding, and user SSH rc execution.

Session port forwarding uses a separate no-PTY forced command and does not enable OpenSSH TCP forwarding. After redeeming a device- and SSH-key-bound one-time token, one HTTP/2 tunnel carries CONNECT streams for exactly one authorized runtime loopback port. The privileged Runtime Helper resolves the managed session network namespace itself and returns only an already-connected socket FD over `SCM_RIGHTS`; clients cannot provide a host, IP, PID, namespace path, or container ID. Heartbeats currently advertise this capability for Native Runtime only. Docker Sandbox remains disabled for forwarding until an equivalent audited network-namespace path is available.

Managed macOS device control advertises `observation_mode_v2`, `ax_state_v2`, and
`adaptive_settle_v2` only when the Native Runtime and verified device proxy are
available. The Server selects the complete set or an empty v1 fallback; partial
sets and same-generation capability downgrades are rejected. New generations use
the complete v2 set by default, while the Server emergency switch forces the empty
v1 set. Runtime Helper writes
the negotiated set into the owner-only managed context, starts the proxy with the
four-tool compact MCP surface, and fixes zero-content optimization metrics at
`/tmp/agent-remote-device-optimization.jsonl` inside the isolated session.

Native account binding requires a registered device token and an active SSH key. Binding attach uses the same forced-command gateway as normal sessions and is re-authorized by the control plane on every connection.

`create_browser_session` node tasks start a temporary Kasm Chrome container by default. The browser runtime receives timezone, locale, launch URL, incognito Chrome arguments, and a temporary VNC password. It does not mount workspace or tool-account directories. `stop_browser_session` removes the container and the temporary profile directory under `browser_root`.

## Config

`register` writes the node token to the configured JSON file:

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.2.9",
  "supported_tool_types": ["claude"],
  "heartbeat_interval_seconds": 30,
  "poll_interval_seconds": 5,
  "ledger_path": "./agent-remote-node-ledger.json",
  "ssh_authorized_keys_path": "./authorized_keys.agent-remote",
  "attach_binary_path": "agent-remote-attach",
  "workspace_root": "/var/lib/agent-remote/users",
  "account_root": "/var/lib/agent-remote/users",
  "docker_binary_path": "docker",
  "tmux_binary_path": "tmux",
  "mutagen_binary_path": "mutagen",
  "browser_root": "/var/lib/agent-remote/browser-sessions",
  "browser_image": "kasmweb/chrome:1.18.0",
  "browser_public_base_url": "",
  "browser_docker_network": "",
  "allowed_runtime_backends": ["docker_sandbox", "native"],
  "runtime_socket_path": "/run/agent-remote/runtime.sock",
  "runtime_binary_path": "/usr/local/bin/agent-remote-runtime",
  "claude_runtime_path": "/opt/agent-remote/runtimes/claude/current/bin/claude",
  "device_proxy_path": "/opt/agent-remote/device/current/bin/agent-remote-device-proxy"
}
```

The config file contains node credentials and must be stored with deployment-level file permissions.

No public listener, Docker port publish, NAT rule, or dynamic WireGuard ACL is created for session forwards. The existing restricted SSH port is the only data-plane entry. Runtime Helper and node services must be upgraded before enabling the control-plane policy.

`browser_public_base_url` is optional. When it is empty, the node reports the local Docker port mapping for KasmVNC. In deployed environments, set it to the node-side HTTPS reverse-proxy URL that reaches the browser container stream endpoint.

For a control plane and node running on the same Docker host, set `browser_docker_network` to the control-plane Compose network (for example `agent-remote_default`). Browser containers then join that private network and the control plane reaches KasmVNC by container DNS without exposing its port on the host.

## One-command Install

Create the node in the admin console, then run one command on a clean Debian 12+ or Ubuntu 22.04+ VPS:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token>
```

This installs missing native runtime dependencies without upgrading packages that are already installed, enables IPv4 forwarding and user namespaces, configures the restricted SSH gateway, downloads Claude Code `latest` through Anthropic's official installer, and installs the latest verified Node.js 22 release with `npm` and `npx` into the same read-only managed runtime. It records both runtime versions and SHA256 checksums, registers the node, starts both systemd services, and verifies the runtime probe and control-plane heartbeat. The default backend is `native`, so KVM and Docker are not required. Run it as root, or as a user that has `sudo` access; the installer elevates only the system operations.

The default native dependency set also provides a consistent AI development baseline on minimal VPS images: standard shell/text/file utilities, `rg`, `jq`, Git/Git LFS/GitHub CLI, archive tools, `rsync`, Python 3 with pip and venv, SQLite, a C/C++ build toolchain, and common process/network/DNS diagnostics. The installer verifies the commands after package installation and repairs a broken `awk` alternatives link by reinstalling `gawk`. These host tools are exposed read-only inside Native sessions and do not grant additional privileges.

The command is idempotent. Re-running it upgrades the node binaries, Claude, and the selected Node.js release line, refreshes the system layout, and reuses the existing node token. Add `--force-register` only when intentionally replacing the node registration.

Install a specific node release or pin the official Claude version:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- \
  --version <node-version> \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token> \
  --claude-version <claude-version>
```

For a supply-chain-pinned Claude artifact, add all three options:

```sh
--claude-version <version> --claude-source <artifact-or-url> --claude-sha256 <sha256>
```

Node.js defaults to the latest verified release in the 22.x line. Pin an official version with `--nodejs-version`, or pin a supplied archive with all three options:

```sh
--nodejs-version <version> --nodejs-source <archive-or-url> --nodejs-sha256 <sha256>
```

The installer fails before enabling the worker when the host does not satisfy Linux 5.15+, systemd 249+, cgroup v2, Bubblewrap user namespaces, or the required locale. To install files without registration or startup, omit the three control-plane options and add `--no-start`. To retain Docker Sandbox compatibility, use `--runtime-backends native,docker_sandbox`; this requires an already installed Docker CLI that provides `docker sandbox`.

Install from an extracted release archive with the same one-command options:

```sh
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## Release Packaging

Node and Device use independent release versions. `release-dependencies.json` pins the exact
Device proxy tag, commit, and artifact-signing workflow embedded by a Node release. Updating that dependency is an explicit,
reviewable source change; preparing a new Node version does not rewrite it.

Linux packages require an architecture- and libc-matched managed device proxy at:

```text
$DEVICE_PROXY_DIR/linux-amd64-glibc/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-arm64-glibc/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-amd64-musl/agent-remote-device-proxy
$DEVICE_PROXY_DIR/linux-arm64-musl/agent-remote-device-proxy
$DEVICE_PROXY_DIR/<target>/VERSION
```

The installer verifies its digest, installs it under
`/opt/agent-remote/device/releases/<version>/`, and atomically switches `current`. Reusing a
version with different bytes is rejected, and capability remains disabled if the proxy is absent
or not executable.

```sh
VERSION=0.2.9 DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
```

The release flow builds six archives: `darwin-amd64`, `darwin-arm64`, `linux-amd64-glibc`, `linux-arm64-glibc`, `linux-amd64-musl`, and `linux-arm64-musl`. The Go binaries are built with `CGO_ENABLED=0`; the glibc and musl labels exist so installers and users can select packages by deployment environment.

Each archive includes node binaries, installer, systemd unit, sample config, license, and notices.
Linux archives also include the managed device proxy.

GitHub Actions runs this packaging flow for `v*` tags and uploads the archives to the GitHub Release.

## License

agent-remote-node is licensed under GPL-3.0-only. See `LICENSE`.

Third-party dependency notices are listed in `THIRD_PARTY_NOTICES.md`.
