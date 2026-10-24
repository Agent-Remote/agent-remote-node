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

Binding attachment with an expected attempt must preserve that identity from the SSH
original command through Server verification and runtime dispatch. Reject malformed,
missing or mismatched attempt replies before invoking sudo/runtime; never substitute
the currently newest account binding or enable agent forwarding for account login.
The exact flow also requires the actual SSH key ID from the trusted authorized_keys
forced command. Do not accept that identity from SSH_ORIGINAL_COMMAND; the Server checks
that it is still active and belongs to the authenticated device. Legacy attachment
without an expected attempt keeps its existing compatibility behavior.

## Backend Compatibility

Docker Sandbox support requires bounded, successful command-specific help for create, exec and rm.
An exit-zero removal notice is not support. New runtime/binding starts recheck before host mutation;
failed observation preserves recovery data. Skill capability is separately gated by the rules below.

## Skill Manager

The mandatory Node lifecycle, storage and recovery rules are centralized in
[Skill manager](../skill-manager.md) and its linked cross-repository contract. Read them before changing
Skill code. Preserve exact original bindings, private Helper authority, writer/reader exclusion,
immutable receipts and retention; do not reconstruct authorization from historical task success.

Binding gateway capability probes use a fixed executable argument, a two-second
deadline, a 256-byte stdout cap, discarded stderr, and process-group cancellation.
Missing, incompatible, oversized, nonzero-exit or timed-out gateways never advertise
exact binding support. Probe the configured gateway again on each runtime report.

Sync authorization must include the forced-command SSH key ID, never a value from
SSH_ORIGINAL_COMMAND. Reject a missing or changed Server key echo before sudo or
runtime dispatch; do not fall back to device-only verification.

Exact session attachment follows [its strict protocol](../session-exact-attach.md).
Compare the expected device locally before HTTP, require the actual forced SSH key,
reject redirects and incomplete/mismatched verification, and probe the installed
gateway with `--session-protocol-info` independently of binding support on every
runtime report. Never fall back to the legacy session verification route.
