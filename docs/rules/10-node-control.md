# 10 Node Control

- Heartbeats and reconciliation contain bounded capability and resource summaries, never secret state.
- Poll only with node credentials and lease only tasks assigned by the authenticated control plane.
- Validate task type, identifiers, backend, paths, resource limits, locale, and timing before execution.
- Task execution and terminal result reporting are idempotent by `task_id`; retries must not duplicate resources or destructive effects.
- Cancellation and lease expiry must stop work when the operation can be interrupted safely.
- Reconciliation reports facts and may identify missing sessions; it never replays prior commands.
- Device-control activation and deactivation tasks validate every binding member and generation,
  reject unknown fields, and cannot supply commands, executable paths, relay endpoints, or secrets.
  Generation validation matches the Server `BIGINT` range and reserves its maximum for terminal
  deactivation only.

Privileged helper requests use a versioned local protocol, peer credential authorization, strict operation allowlists, managed-root path checks, and serialized mutation. SSH attach and sync forced commands re-authorize each connection with the server and continue to deny arbitrary commands and forwarding not explicitly approved.
