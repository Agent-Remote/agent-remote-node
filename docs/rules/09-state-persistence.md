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
- Writes that affect authorization, keys, services, or configuration must be atomic where possible and preserve recoverable failure behavior.

State format changes require compatibility tests or a documented migration. Install and upgrade scripts must remain idempotent and must not overwrite valid operator configuration unexpectedly.
