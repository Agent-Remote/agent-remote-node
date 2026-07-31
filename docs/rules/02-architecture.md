# 02 Architecture

## Layout And Privilege Boundary

```text
cmd/agent-remote-node/     Unprivileged worker entrypoint
cmd/agent-remote-attach/   Forced-command SSH gateway
cmd/agent-remote-runtime/  Privileged helper entrypoint
internal/api/              Control-plane client and payloads
internal/config/           Configuration loading and defaults
internal/ledger/           Local task idempotency ledger
internal/worker/           Polling, dispatch, and reconciliation
internal/runtimehelper/    Root-owned Unix socket protocol and engine
internal/runtime/          Capability and resource snapshots
internal/browser/          Browser runtime specifications
internal/workspace/        Managed workspace operations
```

The worker must remain unprivileged and must not join the Docker group. Privileged actions pass through the root-owned Unix socket, peer credential checks, request validation, and serialized engine execution. Packages must not bypass this boundary with direct host mutations.
The worker, privileged helper, and every transient managed runtime explicitly disable core dumps;
device screenshots, input, credentials, and session state must not enter host crash artifacts.

## Local Device Control

The node may advertise a managed device-control proxy capability only after independently
confirming the configured proxy path is a regular executable file and the native runtime and
network namespace probes pass. The capability is protocol-versioned and currently exposes only
`platform=macos` and `backend=native`. Project files, Claude output, and task payloads cannot
override the proxy path or relay endpoint. The node does not parse or persist GUI payloads.

Linux release packages must include the matching `agent-remote-device-proxy` artifact. The
installer verifies its SHA-256 digest, installs immutable version directories under
`/opt/agent-remote/device/releases`, and atomically switches the `current` symlink. Reinstalling
the same version with different bytes is rejected. A missing, non-executable, or replaced proxy
keeps device-control capability disabled.

Eligible native Claude sessions receive a root-generated inline
`--strict-mcp-config --mcp-config` value and inline `Stop`, `StopFailure`, and `SessionEnd` hooks. Device-enabled
sessions load no user, project, or local settings sources, and reject CLI arguments that could
replace settings, disable hooks, or override MCP configuration. Both hooks invoke the verified
proxy binary in exec form over a fixed session-private lifecycle socket. The root helper binds the
verified proxy binary and per-session bridge directory at fixed sandbox paths. Project files,
account configuration, and user-provided CLI arguments cannot replace these paths.

The unprivileged worker owns the per-generation Unix bridge listener, authenticates its fixed
binding against the activation manifest, exchanges proxy relay material with node credentials,
and opens only the server-returned fixed relay path. It forwards opaque nested-TLS bytes in both
directions and never decodes action or screenshot JSON. The Rust proxy performs mutual TLS 1.3,
exact peer pinning, and exporter confirmation before sending an application frame.

Generation values use the control plane's positive signed 64-bit range. Activation and live
context payloads are capped at `9223372036854775806`; deactivation may use the reserved terminal
value `9223372036854775807`. Values outside those bounds are rejected before local state changes.
