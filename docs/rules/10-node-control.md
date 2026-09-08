# 10 Node Control

- Heartbeats and reconciliation contain bounded capability and resource summaries, never secret state.
- Poll only with node credentials and lease only tasks assigned by the authenticated control plane.
- Validate task type, identifiers, backend, paths, resource limits, locale, and timing before execution.
- Task execution and terminal result reporting are idempotent by `task_id`; the ledger replays both
  successful and failed terminal results after a lease is reissued, without repeating runtime work.
- Cancellation and lease expiry must stop work when the operation can be interrupted safely.
- Reconciliation reports facts and may identify missing sessions; it never replays prior commands.
- Device-control activation and deactivation tasks validate every binding member and generation,
  reject unknown fields, and cannot supply commands, executable paths, relay endpoints, or secrets.
  Generation validation matches the Server `BIGINT` range and reserves its maximum for terminal
  deactivation only.
- Full-trust runtime contexts advertise and preserve the complete canonical capability set:
  `observation_mode_v2`, `ax_state_v2`, `adaptive_settle_v2`, `clipboard_payload_v2`,
  `session_full_trust_v1`, `application_launch_v1`, and `global_clipboard_v1`. Partial sets are
  rejected and same-generation reconciliation cannot widen or narrow the set. Legacy
  `per_application_approval` contexts accept only v1, the exact three-name v2 observation base, or
  that base plus `clipboard_payload_v2`; they reject full-trust launch, clipboard, and authorization
  capabilities before writing managed state.
- Ego-browser binding snapshots require `remote_platform=linux`, `local_platform=macos`, the
  independent browser relay kind and channel, a compatible wrapper protocol, positive generation,
  active healthy lease, and a capability set whose allowlist and Site Learning names exactly match
  their verified digests. The wrapper cannot widen this snapshot.
- Ego-browser context is injected into eligible `native` and `docker_sandbox` Claude tool sessions.
  The runtime helper must return the selected non-root runtime UID; the worker strips that internal value, grants the numeric UID a
  narrow POSIX ACL on the broker directory/socket, and the broker matches it with Linux
  `SO_PEERCRED`. UID 0 is ineligible. Docker uses a root-owned trusted spec, executes as the fixed
  UID/GID, mounts the verified wrapper/Skill/broker paths, and never persists or places the nonce in argv.
- The broker enforces the lower of Node and binding parallelism limits, strict wire-order sequence
  submission, one-time permit consumption, admission-time lease renewal, and immediate conflict
  errors. Disconnect or unknown results are never retried automatically.

Privileged helper requests use a versioned local protocol, peer credential authorization, strict operation allowlists, managed-root path checks, and serialized mutation. SSH attach and sync forced commands re-authorize each connection with the server and continue to deny arbitrary commands and forwarding not explicitly approved.
