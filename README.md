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

`agent-remote-node` is the Go runtime deployed on a VPS host. It polls the Agent Remote control plane, executes approved tasks through the runtime helper and never exposes a public inbound control API.

## Runtime boundary

- The unprivileged worker polls the control plane and handles task leases and durable completion records.
- Privileged host work is isolated behind the runtime helper.
- Native sessions use a dedicated Linux identity, systemd limits, Bubblewrap, network namespace, nftables rules, bounded temporary storage and a private tmux socket.
- Docker Sandbox sessions use a non-root session identity and a root-owned trusted spec; the worker never joins the Docker group.
- Forced-command SSH is the only attach path. Session port forwarding uses a separate authorized loopback tunnel and does not enable OpenSSH TCP forwarding.
- Browser sessions use an isolated temporary container and profile; they do not mount a workspace or tool-account directory.

## Commands

Run the checks with:

```sh
go test ./...
go run ./cmd/agent-remote-node --help
```

Managed enrollment starts from a logged-in control workstation:

```sh
agent-remote node install --node <node-id-or-prefix>
```

The CLI verifies the pinned Node archive, transfers it over an independent SSH stdin channel, requests a one-time join code and sends that code in a separate SSH request. New Nodes keep ego-browser disabled unless the administrator explicitly supplies `--enable-ego-browser`; reinstalling an existing Node preserves its setting.

The direct registration command is an advanced compatibility path:

```sh
go run ./cmd/agent-remote-node register --config ./config.json \
  --server-url http://localhost:8000 --node-id <node-id> \
  --registration-token <registration-token>
go run ./cmd/agent-remote-node heartbeat --config ./config.json
go run ./cmd/agent-remote-node poll-once --config ./config.json
go run ./cmd/agent-remote-node run --config ./config.json
go run ./cmd/agent-remote-node install-ssh --config ./config.json
```

`agent-remote-attach` requires an exact session/device or binding/device identity and performs a dry-run before an interactive attach.

Install the latest verified node archive on a Linux host with the repository
installer:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- --server-url https://agent-remote.example.com \
  --node-id <node-id> --registration-token <token>
```

Pin a release with `--version <version>`. The installer verifies the archive,
configures the selected runtime backend and starts services only after all
probes pass. Build archives with `DEVICE_PROXY_DIR=/path/to/device-proxies
scripts/build-release.sh`; checksums and packages do not replace host, SSH or
broker acceptance.

Install a specific release or an extracted archive:

```sh
curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
  bash -s -- --version <node-version> \
  --server-url https://agent-remote.example.com \
  --node-id <node-id> --registration-token <registration-token> \
  --claude-version <claude-version>
./install.sh --server-url <url> --node-id <id> --registration-token <token>
```

## Configuration and state

The JSON config contains the server URL, Node identity, polling intervals, ledger path, SSH paths, workspace/account roots, runtime backend policy and protected helper paths. It contains a Node credential and must be owner-readable only.

Workspace preparation and synchronization bind the requested path to the authorized user, workspace, device and SSH key. Paths, identifiers, runtime backends, resource limits, symlinks, hard links and special files are validated before any privileged operation. Skill state, runtime state, browser state and workspace data use separate protected roots.

Native and Docker backends report their actual capabilities. The server may negotiate the Device v2 capability set only when the backend and device proxy probes pass; partial or unknown capability sets fail closed.

## Development and release

Go 1.26.6 or newer is required. Run:

```sh
bash -n scripts/*.sh
gofmt -l .
go vet ./...
go test ./...
scripts/run-quality-checks.sh
```

Use disposable server/node homes and remove test containers, temporary keys and browser profiles after acceptance. Release archives are built by `scripts/build-release.sh`; package and checksum verification do not replace native runtime, SSH, broker or remote-session evidence.

## Script entry points

The repository keeps these installation, runtime packaging and acceptance
entry points:

```sh
# Build a release archive (DEVICE_PROXY_DIR supplies signed device proxies).
DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh
# Prepare release metadata and run package/source checks.
scripts/prepare-release.sh
python3 scripts/generate-release-policy.py --help
scripts/check-ego-browser-skill-source.sh
scripts/check-managed-skills.sh
scripts/check-coverage.sh
# Install the node and its optional managed runtimes.
scripts/install.sh --server-url <url> --node-id <id> --registration-token <token>
scripts/install-claude-runtime.sh
scripts/install-nodejs-runtime.sh
scripts/install-device-proxy.sh
scripts/install-ego-browser-runtime.sh
# Install repository hooks for local development.
scripts/install-githooks.sh
# Host-only filesystem and configuration-import acceptance.
bash tests/linux_native_filesystem_test.sh
bash tests/linux_config_import_test.sh
```

`install.sh` is the supported host installer. The runtime and device-proxy
installers are called by that flow or by an administrator performing an
explicit component upgrade; they do not register a node on their own.

## Documentation

- Runtime and package rules: `docs/rules/02-architecture.md`, `docs/rules/09-state-persistence.md`, `docs/rules/10-node-control.md`
- Terminal behavior: `docs/terminal-interaction.md`
- Session and attachment contracts: `docs/session-lifecycle.md`, `docs/session-exact-attach.md`, `docs/session-exact-attachment-transfer.md`
- Workspace safety: `docs/workspace-sync-safety.md`
- Ego-browser runtime: `docs/ego-browser-artifacts.md`, `docs/ego-browser-operations.md`
- Skills: `docs/skill-manager.md`

These documents describe current behavior. Code, tests and the control-plane contract take precedence if a note is stale.

## License

GPL-3.0-only. See `LICENSE` and `THIRD_PARTY_NOTICES.md`.
