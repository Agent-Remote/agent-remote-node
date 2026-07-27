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
