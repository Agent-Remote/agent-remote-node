# 01 Project Overview

`agent-remote-node` is the outbound, poll-based node runtime for agent-remote. It reports capacity, leases control-plane tasks, reconciles runtime state, and executes approved operations through a narrowly scoped privileged helper.

## Repository Boundary

- The node does not expose a public inbound HTTP API.
- The server owns authentication, authorization, task scheduling, and runtime policy.
- The worker owns polling and orchestration; the runtime helper owns privileged host mutations.
- Tool login state, browser profiles, runtime resources, and account archives remain node-local.

Changes to task payloads or results require coordinated server schemas, node validation, idempotency behavior, and tests.
