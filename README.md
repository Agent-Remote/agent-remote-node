# agent-remote-node

Node-side runtime for agent-remote.

The node runs on a VPS and talks to `agent-remote-server` by polling the control plane. It does not expose public HTTP ports.

## Commands

```sh
go test ./...
```

```sh
go run ./cmd/agent-remote-node --help
```

```sh
go run ./cmd/agent-remote-node register \
  --config ./config.json \
  --server-url http://localhost:8000 \
  --node-id <node-id> \
  --registration-token <registration-token>
```

```sh
go run ./cmd/agent-remote-node heartbeat --config ./config.json
```

```sh
go run ./cmd/agent-remote-node poll-once --config ./config.json
```

```sh
go run ./cmd/agent-remote-node run --config ./config.json
```

## Config

`register` writes the node token to the configured JSON file:

```json
{
  "server_url": "http://localhost:8000",
  "node_id": "00000000-0000-0000-0000-000000000000",
  "node_token": "node_...",
  "version": "0.1.0",
  "supported_tool_types": ["claude"],
  "heartbeat_interval_seconds": 30,
  "poll_interval_seconds": 5,
  "ledger_path": "./agent-remote-node-ledger.json"
}
```

The config file contains node credentials and must be stored with deployment-level file permissions.

