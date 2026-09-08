# 09 State And Persistence

- Configuration contains node credentials and requires deployment-controlled permissions.
- Registration writes credentials atomically and must not disclose them in output.
- The ledger stores only bounded local task execution metadata needed for idempotency.
- Tool login state, browser profiles, account archives, and session resources stay under configured managed roots.
- Runtime identities and paths derive from validated server IDs and configured roots, never unchecked task paths.
- Device-control activation manifests contain binding and generation metadata only. They are
  atomically stored as owner-only files beneath `device_control_root`; connection tickets,
  certificate pins, exporter data, screenshots, and action payloads are never persisted there.
- Relay tickets, peer certificate pins, and exporter contexts exist only in the live bridge call
  stack and are discarded when the generation listener closes.
- Ego-browser state persists only the binding/generation-scoped next sequence needed for replay
  prevention. Relay tickets, request permits, session keys, key wraps, ciphertext, heredoc text,
  browser output, artifacts, and device credentials remain in bounded process memory and are
  cleared on broker restart or generation change.
- Browser-request cancellation stores only its exact three-field completion result or a fixed
  content-free failure in the task ledger; unrestricted broker error text is never persisted.
- Ego-browser broker lifecycle logs use finite error codes; socket, filesystem, protocol, and
  relay error text is never rendered into operational logs.
- Managed official Skill and wrapper artifacts are installed into immutable version directories
  from a release manifest that pins source provenance and SHA-256 digests. Before either backend
  starts Claude, the helper verifies those sources and refreshes the account's managed Skill copy;
  runtime projects cannot replace the verified host artifacts or injected wrapper-first PATH entry.
- Each Docker Sandbox resource has a root-owned, owner-only trusted spec containing its kind,
  runtime UID/GID, tmux and sandbox identities, and enabled feature paths. Nonces remain process-only.
  Binding specs are excluded from tool-session reconciliation and cannot carry tool-session features.
- Writes that affect authorization, keys, services, or configuration must be atomic where possible and preserve recoverable failure behavior.

State format changes require compatibility tests or a documented migration. Install and upgrade scripts must remain idempotent and must not overwrite valid operator configuration unexpectedly.
