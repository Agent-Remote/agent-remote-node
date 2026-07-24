# agent-remote-node

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

`install-ssh` prepares the managed `authorized_keys` file. Runtime SSH keys are written by the `sync_ssh_keys` node task with forced-command restrictions.

`prepare_workspace` tasks install the device's stable SSH gateway key and ask the privileged runtime helper to create the workspace as the control-plane user's Linux UID. Mutagen commands are re-authorized by device and node, then run without network access in a Bubblewrap view containing only that user's data.

`create_binding_session` and `create_tool_session` use the backend pinned by the control plane. Docker Sandbox remains supported. Native sessions run the managed Claude binary under a per-user UID with systemd cgroup limits, Bubblewrap filesystem isolation, a dedicated network namespace, nftables egress filtering, a quota-limited temporary filesystem, and a per-session tmux socket. Docker and browser operations also pass through the root helper; the node worker is not a member of the Docker group.

Native account binding requires a registered device token and an active SSH key. Binding attach uses the same forced-command gateway as normal sessions and is re-authorized by the control plane on every connection.

`create_browser_session` node tasks start a temporary Kasm Chrome container by default. The browser runtime receives timezone, locale, launch URL, incognito Chrome arguments, and a temporary VNC password. It does not mount workspace or tool-account directories. `stop_browser_session` removes the container and the temporary profile directory under `browser_root`.

## Config

`register` writes the node token to the configured JSON file:

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.0.4-fix.11",
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
  "claude_runtime_path": "/opt/agent-remote/runtimes/claude/current/bin/claude"
}
```

The config file contains node credentials and must be stored with deployment-level file permissions.

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

This installs missing native runtime dependencies without upgrading packages that are already installed, enables IPv4 forwarding and user namespaces, configures the restricted SSH gateway, downloads Claude Code `stable` through Anthropic's official installer, records its version and SHA256 in the managed runtime, registers the node, starts both systemd services, and verifies the runtime probe and control-plane heartbeat. The default backend is `native`, so KVM and Docker are not required. Run it as root, or as a user that has `sudo` access; the installer elevates only the system operations.

The command is idempotent. Re-running it upgrades binaries and Claude, refreshes the system layout, and reuses the existing node token. Add `--force-register` only when intentionally replacing the node registration.

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

The installer fails before enabling the worker when the host does not satisfy Linux 5.15+, systemd 249+, cgroup v2, Bubblewrap user namespaces, or the required locale. To install files without registration or startup, omit the three control-plane options and add `--no-start`. To retain Docker Sandbox compatibility, use `--runtime-backends native,docker_sandbox`; this requires an already installed Docker CLI that provides `docker sandbox`.

Install from an extracted release archive with the same one-command options:

```sh
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## Release Packaging

```sh
VERSION=0.0.4-fix.11 scripts/build-release.sh
```

The release flow builds six archives: `darwin-amd64`, `darwin-arm64`, `linux-amd64-glibc`, `linux-arm64-glibc`, `linux-amd64-musl`, and `linux-arm64-musl`. The Go binaries are built with `CGO_ENABLED=0`; the glibc and musl labels exist so installers and users can select packages by deployment environment.

Each archive includes node binaries, installer, systemd unit, sample config, license, and notices.

GitHub Actions runs this packaging flow for `v*` tags and uploads the archives to the GitHub Release.

## License

agent-remote-node is licensed under GPL-3.0-only. See `LICENSE`.

Third-party dependency notices are listed in `THIRD_PARTY_NOTICES.md`.
