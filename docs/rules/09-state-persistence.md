# 09 State And Persistence

- Configuration contains node credentials and requires deployment-controlled permissions.
- Registration writes credentials atomically and must not disclose them in output.
- The ledger stores only bounded local task execution metadata needed for idempotency.
- Tool login state, browser profiles, account archives, and session resources stay under configured managed roots.
- Runtime identities and paths derive from validated server IDs and configured roots, never unchecked task paths.
- Writes that affect authorization, keys, services, or configuration must be atomic where possible and preserve recoverable failure behavior.

State format changes require compatibility tests or a documented migration. Install and upgrade scripts must remain idempotent and must not overwrite valid operator configuration unexpectedly.
