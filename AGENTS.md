# AGENTS.md

This document is the primary instruction set for AI agents and automated coding tools working in this repository. Repository-local rules take precedence over general assumptions.

## Task-To-Documentation Mapping

Before making changes, identify the task domain and read the matching rule document.

| Task Domain | Primary Reference |
| --- | --- |
| Project purpose and repository boundary | `docs/rules/01-project-overview.md` |
| Runtime architecture and package boundaries | `docs/rules/02-architecture.md` |
| Go toolchain and runtime dependencies | `docs/rules/03-tech-stack.md` |
| Go style, errors, concurrency, and tests | `docs/rules/04-code-style.md` |
| Comments, logs, and internal documentation | `docs/rules/05-comments-logging.md` |
| Local commands and developer workflow | `docs/rules/06-commands.md` |
| Quality and security gates | `docs/rules/07-quality-security.md` |
| Git, commits, hooks, and pull requests | `docs/rules/08-collaboration.md` |
| Configuration, ledger, and filesystem state | `docs/rules/09-state-persistence.md` |
| Polling, task execution, and privileged runtime | `docs/rules/10-node-control.md` |

## Mandatory Gates

- Shell syntax checks, `gofmt`, `go vet`, all Go tests, installer tests, and `git diff --check` must pass before commit.
- Exported Go declarations must have concise Go-style comments.
- Task execution and completion reporting must remain idempotent by `task_id`.
- Commit messages must follow Conventional Commits.
- Node tokens, registration tokens, cookies, private keys, and tool login state must never be committed or logged.

## Implementation Rules

- Keep the node outbound and poll-based; do not add a public inbound HTTP API.
- Keep control-plane calls in `internal/api` and privileged host operations behind `internal/runtimehelper`.
- The unprivileged worker must not gain Docker group membership or direct privileged filesystem access.
- Preserve forced-command SSH authorization and re-authorize attach and sync operations with the control plane.
- Treat task payloads as untrusted input and validate paths, identifiers, runtime backends, and resource bounds.
- Prefer the Go standard library and explicit packages over speculative abstractions.

## Hook Setup

Install repository hooks after cloning:

```sh
scripts/install-githooks.sh
```

Run the full local quality gate:

```sh
scripts/run-quality-checks.sh
```

## Conflict Resolution

If existing code conflicts with these rules:

1. Stop before editing the conflicting area.
2. Identify the file and rule that disagree.
3. Ask for the intended current standard.
