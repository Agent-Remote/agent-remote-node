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

`create_binding_session` and `create_tool_session` use the backend pinned by the control plane. Native sessions run the managed Claude binary under a per-user UID with systemd cgroup limits, Bubblewrap filesystem isolation, a dedicated network namespace, nftables egress filtering, a quota-limited temporary filesystem, and a per-session tmux socket. Docker Sandbox sessions run as the Node service's fixed non-root UID/GID, with owned account/workspace paths and numeric ACL access. Docker lifecycle state is recorded in a root-owned trusted spec, and Docker operations remain behind the privileged helper; the Node worker is not a member of the Docker group.

Developer credential profiles provide persistent Git and GitHub CLI configuration directories for both runtime backends. SSH private keys remain on the client: only an authorized `ssh -A` attach is bridged through a session-local Unix socket, and only while that connection is active. Native and Docker Sandbox attach both resolve the tmux target and SSH mode from root-owned runtime state instead of control-plane resource names. The gateway continues to deny TCP forwarding, X11 forwarding, and user SSH rc execution.

Session port forwarding uses a separate no-PTY forced command and does not enable OpenSSH TCP forwarding. After redeeming a device- and SSH-key-bound one-time token, one HTTP/2 tunnel carries CONNECT streams for exactly one authorized runtime loopback port. The privileged Runtime Helper resolves either the Native network namespace or the root-owned Docker Sandbox spec and returns only an already-connected socket FD over `SCM_RIGHTS`; clients cannot provide a host, IP, PID, namespace path, sandbox name, or container ID. Heartbeats advertise this capability for every healthy enabled backend.

Managed macOS device control advertises the required `observation_mode_v2`,
`ax_state_v2`, and `adaptive_settle_v2` base plus the optional
`clipboard_payload_v2` extension only when at least one enabled runtime backend and the verified device
proxy are available. The advertised backend list is exact: Native additionally requires its network
namespace probe, while Docker Sandbox requires its full runtime probe. The Server selects the complete required base, includes
supported extensions, or uses an empty v1 fallback; partial sets and
same-generation capability changes are rejected. New generations use the
supported v2 set by default, while the Server emergency switch forces the empty
v1 set. Runtime Helper writes
the negotiated set into the owner-only managed context, starts the proxy with the
four-tool compact MCP surface, and fixes zero-content optimization metrics at
`/tmp/agent-remote-device-optimization.jsonl` inside the isolated session.

Account binding on either runtime backend requires a registered device token and an active SSH key. Binding attach uses the same forced-command gateway as normal sessions, is re-authorized by the control plane on every connection, and reaches Docker tmux only through the trusted helper.

`create_browser_session` node tasks start a temporary Kasm Chrome container by default. The browser runtime receives timezone, locale, launch URL, incognito Chrome arguments, and a temporary VNC password. It does not mount workspace or tool-account directories. `stop_browser_session` removes the container and the temporary profile directory under `browser_root`.

## Config

The immutable wrapper/Skill source contract is documented in
`docs/ego-browser-artifacts.md`; runtime UID/ACL diagnostics, metrics, and
recovery are in `docs/ego-browser-operations.md`. Eligible Claude tool sessions on both
Native and Docker Sandbox receive a runtime-scoped broker capability. Docker startup verifies and
mounts the release-pinned artifacts, refreshes the exact managed Skill tree in the account, prepends
the wrapper directory to `PATH`, and passes the nonce only through the process environment.

`register` writes the node token to the configured JSON file:

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.2.18",
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

On Linux, the installer automatically runs `configure-ego-browser` after installing the managed
runtime. It updates the wrapper and Skill paths, versions, and digest from the immutable `current`
release while preserving the existing `ego_browser_enabled` value. Installing or upgrading the
artifact does not implicitly enable the bridge. To synchronize an independently installed runtime,
run:

```sh
sudo agent-remote-node configure-ego-browser \
  --config /etc/agent-remote-node/config.json \
  --runtime-root /opt/agent-remote/ego-browser
```

Only enable the bridge after the Server evidence and artifact-bound canary have passed:

```sh
sudo agent-remote-node configure-ego-browser \
  --config /etc/agent-remote-node/config.json \
  --runtime-root /opt/agent-remote/ego-browser \
  --enable
sudo systemctl restart agent-remote-runtime.service agent-remote-node.service
```

Native session specifications now carry a root-generated, non-sensitive runtime configuration
snapshot. The supervisor and Claude child therefore do not need to read the protected node config,
so dynamic session users can start normally even when `/etc/agent-remote-node/config.json` is
owner-only. Upgrade the Runtime Helper and Node binaries together; existing root-owned session
specifications remain usable across later node configuration changes.

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

This installs the dependencies required by the selected backends without upgrading packages that are already installed, configures the restricted SSH gateway, installs the managed device proxy, registers the node, starts both systemd services, and verifies the runtime probe and control-plane heartbeat. With the default `native` backend it also enables IPv4 forwarding and user namespaces, downloads Claude Code `latest` through Anthropic's official installer, and installs the latest verified Node.js 22 release with `npm` and `npx` into the same read-only managed runtime. The default does not require KVM or Docker. Run it as root, or as a user that has `sudo` access; the installer elevates only the system operations.

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

The installer fails before enabling the worker when a selected backend does not satisfy its probe. Native requires Linux 5.15+, systemd 249+, cgroup v2, Bubblewrap user namespaces, and the configured locale. Docker Sandbox requires Linux, a root helper, tmux, Git, POSIX ACL tools, a valid non-root runtime identity, and an already installed Docker CLI whose daemon and `docker sandbox` command are available. Use `--runtime-backends native,docker_sandbox` for both or `--runtime-backends docker_sandbox` for Docker only. To install files without registration or startup, omit the three control-plane options and add `--no-start`.

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
VERSION=0.2.18 DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
```

The release flow builds six archives: `darwin-amd64`, `darwin-arm64`, `linux-amd64-glibc`, `linux-arm64-glibc`, `linux-amd64-musl`, and `linux-arm64-musl`. The Go binaries are built with `CGO_ENABLED=0`; the glibc and musl labels exist so installers and users can select packages by deployment environment.

Each archive includes node binaries, installer, systemd unit, sample config, license, and notices.
Linux archives also include the managed device proxy.

GitHub Actions runs this packaging flow for `v*` tags and uploads the archives to the GitHub Release.

## License

agent-remote-node is licensed under GPL-3.0-only. See `LICENSE`.

Third-party dependency notices are listed in `THIRD_PARTY_NOTICES.md`.
