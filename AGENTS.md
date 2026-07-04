# AGENTS.md

This repository contains the Go node-side runtime for agent-remote.

## Rules

- Use Go standard library first.
- Keep node behavior poll-based; do not add inbound public HTTP APIs.
- Do not log node tokens, registration tokens, cookies, private keys, or tool login state.
- Persist only local task execution metadata in the ledger.
- Task execution must be idempotent by `task_id`.
- Run `gofmt` and `go test ./...` before committing.

## Layout

```text
cmd/agent-remote-node/  CLI entrypoint
internal/api/           Control-plane API client
internal/config/        JSON config loading and saving
internal/ledger/        Local task execution ledger
internal/runtime/       Runtime and resource snapshots
internal/worker/        Task polling and execution loop
```

