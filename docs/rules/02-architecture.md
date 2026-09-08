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
internal/devicecontrol/    Opaque device-control activation and relay bridge
internal/egobrowser/       Node-owned ego-browser broker and relay protocol
internal/egobrowserartifact/ Immutable wrapper and Skill verification
internal/portforward/      Authorized session loopback tunnels
internal/toolaccounts/     Docker Sandbox account-binding runtime
internal/toolsessions/     Docker Sandbox tool-session runtime
internal/workspace/        Managed workspace operations
```

The worker must remain unprivileged and must not join the Docker group. Privileged actions pass through the root-owned Unix socket, peer credential checks, request validation, and serialized engine execution. Packages must not bypass this boundary with direct host mutations.
The worker persists successful and failed task outcomes in its local idempotency ledger. When the
control plane reissues an expired lease, the worker reports that stored terminal outcome instead
of invoking the privileged runtime operation again.
The worker, privileged helper, and every transient managed runtime explicitly disable core dumps;
device screenshots, input, credentials, and session state must not enter host crash artifacts.

## Local Device Control

The node may advertise a managed device-control proxy capability only after independently
confirming the configured proxy path is a regular executable file. The capability is
protocol-versioned, exposes `platform=macos`, and lists each healthy enabled runtime backend:
Native requires its network namespace probe, while Docker Sandbox requires its full runtime probe.
Project files, Claude output, and task payloads cannot
override the proxy path or relay endpoint. The node does not parse or persist GUI payloads.

Linux release packages must include the matching `agent-remote-device-proxy` artifact. The
installer verifies its SHA-256 digest, installs immutable version directories under
`/opt/agent-remote/device/releases`, and atomically switches the `current` symlink. Reinstalling
the same version with different bytes is rejected. A missing, non-executable, or replaced proxy
keeps device-control capability disabled.

Eligible Native and Docker Sandbox Claude sessions receive a root-generated inline
`--strict-mcp-config --mcp-config` value and inline `Stop`, `StopFailure`, and `SessionEnd` hooks. They retain the
account's complete user settings source while rejecting CLI arguments that could replace settings,
disable managed hooks, or override MCP configuration. The runtime helper also installs and updates
the Agent Remote-owned `agent-remote-device` skill before account binding and session startup; it
does not modify other account skills or configuration. Both hooks invoke the verified
proxy binary in exec form over a fixed session-private lifecycle socket. For Native, the root helper
read-only binds the verified proxy binary and per-session bridge directory at fixed Bubblewrap paths.
For Docker Sandbox, it records those paths in the root-owned trusted spec, mounts them through the
Docker Sandbox lifecycle, and restores numeric traversal ACLs after persisting the spec. Project files,
account configuration, and user-provided CLI arguments cannot replace these paths.
Node CI must compare the managed skill against the exact reviewed Device commit recorded in
`managed-skill-source.json`, and a Node release must compare it against the matching Device
version tag. Updating the embedded skill and its source commit is one atomic change. Tests must
also verify that the runtime writes the complete embedded skill tree into the Claude account
without content drift.

The unprivileged worker owns the per-generation Unix bridge listener, authenticates its fixed
binding against the activation manifest, exchanges proxy relay material with node credentials,
and opens only the server-returned fixed relay path. It forwards opaque nested-TLS bytes in both
directions and never decodes action or screenshot JSON. The Rust proxy performs mutual TLS 1.3,
exact peer pinning, and exporter confirmation before sending an application frame.

Generation values use the control plane's positive signed 64-bit range. Activation and live
context payloads are capped at `9223372036854775806`; deactivation may use the reserved terminal
value `9223372036854775807`. Values outside those bounds are rejected before local state changes.

## Ego Browser Broker

The unprivileged Node daemon owns a separate per-tool-session ego-browser broker. Eligible Linux
Claude runtimes receive only an owner-only Unix socket path, a process-local capability nonce bound
to their exact `tool_session_id`, and non-secret workflow defaults. They never receive or submit a
binding ID, relay URL, ticket, device credential, long-term key, user identity, or local browser
path. The broker resolves the current binding from the nonce after each control-plane refresh, so a
session may be created before the user claims it without becoming ambiguous when other users have
bindings on the same Node. Session stop removes the capability, and broker restart invalidates all
previously issued nonces. The broker derives `agent-remote:<tool_session_id>` from that nonce,
returns the dedicated Task Space in every one-use permit, and rejects a different Task Space lock
scope. The wrapper therefore cannot redirect the normal Skill workflow with an environment
override.

The broker is the only remote endpoint that redeems a wrapper-role ticket and opens the outbound
`ego_browser_bridge` relay. It allocates persistent monotonic sequences, issues one-use bounded
permits, wraps per-request session keys to the registered local Bridge key, serializes WebSocket
writes, and dispatches out-of-order responses by the complete binding, generation, request, and
sequence identity. Cancellation keeps bounded tombstones so a late response cannot corrupt a
later request. Reload, revoke, generation or policy drift, relay failure, and shutdown clear all
pending material without replaying heredoc scripts.

Official `ego-browser` Skill content and the Linux remote wrapper are immutable, release-pinned
managed artifacts. Runtime PATH selects the wrapper; no remote browser is installed or launched.
The broker is independent of all local GUI device-control packages, identities, roots, and routes.

The wrapper is available to eligible Claude tool sessions on both runtime backends. Native uses its
dedicated per-user identity; Docker Sandbox uses the configured Node service identity as a fixed
non-root UID/GID and executes the sandbox process with `-u UID:GID`. The privileged helper returns
the selected numeric UID over the root-owned helper socket. The
worker removes that internal field from the task result, starts the broker socket as `0600` below a
`0700` directory, and grants only directory traverse plus socket read/write through numeric POSIX
ACL entries. The broker then requires the session nonce and the exact kernel `SO_PEERCRED` UID on
every wrapper connection. UID 0 is rejected. Docker startup validates and mounts the pinned wrapper,
Skill source, and broker socket directory, refreshes the canonical embedded Skill tree into the
account, prepends the wrapper directory to the process `PATH`, and passes the nonce through the
process environment rather than command arguments. Its root-owned spec never stores the nonce.

`tests/linux_ego_browser_uid_acl_test.sh` cross-compiles the real broker test and runs it in a
rootful Linux container with `acl` installed. It checks the exact directory and socket ACLs, proves
the authorized non-root UID connects, proves an unlisted UID is blocked by the filesystem, then
temporarily grants that UID filesystem access and proves `SO_PEERCRED` still rejects it.
