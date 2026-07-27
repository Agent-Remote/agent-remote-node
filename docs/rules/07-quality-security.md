# 07 Quality And Security

CI, pre-commit, and pre-push enforce shell parsing, formatting, vetting, Go tests, installer tests, and whitespace checks.

- Treat configuration, task payloads, API responses, archives, filesystem state, and helper requests as untrusted.
- Validate managed-root containment without relying on string prefixes or unresolved symlinks.
- Keep the runtime socket root-owned and verify peer credentials.
- Use bounded timeouts and output sizes for external commands.
- Do not pass secrets in command-line arguments when a protected file or descriptor is available.
- Preserve Bubblewrap, cgroup, namespace, nftables, forced-command SSH, and filesystem permission boundaries.
- Never log or return sensitive runtime state.

Reject changes that give the worker ambient privilege, bypass helper validation, or weaken isolation without an explicit architecture and security update.
