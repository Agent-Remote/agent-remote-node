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
```

`install-ssh` prepares the managed `authorized_keys` file. Runtime SSH keys are written by the `sync_ssh_keys` node task with forced-command restrictions.

`prepare_workspace` node tasks create the remote workspace directory under `workspace_root` and write a `.agent-remote-workspace.json` marker file for reconciliation.

`create_binding_session` node tasks create the remote tool-account archive under `account_root`, write a non-secret `.agent-remote-tool-account.json` marker, and create a Docker Sandbox with the built-in `claude` agent. tmux then holds an interactive `docker sandbox exec ... claude login` process. The sandbox exposes the account archive at the same path, and `CLAUDE_CONFIG_DIR` points to `<account>/.claude`, so Claude authentication and settings persist in the account archive. `verify_tool_account` tasks currently include the Claude verifier and only report matched auth paths, never file contents.

`create_browser_session` node tasks start a temporary Kasm Chrome container by default. The browser runtime receives timezone, locale, launch URL, incognito Chrome arguments, and a temporary VNC password. It does not mount workspace or tool-account directories. `stop_browser_session` removes the container and the temporary profile directory under `browser_root`.

## Config

`register` writes the node token to the configured JSON file:

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.0.2",
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
  "browser_public_base_url": ""
}
```

The config file contains node credentials and must be stored with deployment-level file permissions.

`browser_public_base_url` is optional. When it is empty, the node reports the local Docker port mapping for KasmVNC. In deployed environments, set it to the node-side HTTPS reverse-proxy URL that reaches the browser container stream endpoint.

## Systemd Install

Install the latest release directly:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | sudo bash
```

Install a specific version or customize paths:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  sudo bash -s -- --version 0.0.3 --prefix /usr/local --config-dir /etc/agent-remote-node
```

Install from a downloaded release archive:

```sh
sudo ./install.sh
```

The installer installs node binaries, creates `/etc/agent-remote-node`, creates `/var/lib/agent-remote-node`, installs `systemd/agent-remote-node.service` when systemd is enabled, and checks Docker, OpenSSH, tmux, Mutagen, and TUN availability. It can also override the GitHub repository, version, target, OS, architecture, libc label, prefix, config directory, state directory, data directory, service user, systemd installation, and sudo behavior.

After creating a node in the admin console, register it:

```sh
sudo agent-remote-node register \
  --config /etc/agent-remote-node/config.json \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> \
  --registration-token <registration-token>
sudo systemctl enable --now agent-remote-node
```

## Release Packaging

```sh
VERSION=0.0.2 scripts/build-release.sh
```

The release flow builds six archives: `darwin-amd64`, `darwin-arm64`, `linux-amd64-glibc`, `linux-arm64-glibc`, `linux-amd64-musl`, and `linux-arm64-musl`. The Go binaries are built with `CGO_ENABLED=0`; the glibc and musl labels exist so installers and users can select packages by deployment environment.

Each archive includes node binaries, installer, systemd unit, sample config, license, and notices.

GitHub Actions runs this packaging flow for `v*` tags and uploads the archives to the GitHub Release.

## License

agent-remote-node is licensed under GPL-3.0-only. See `LICENSE`.

Third-party dependency notices are listed in `THIRD_PARTY_NOTICES.md`.
