# Ego Browser Node Operations

## Runtime boundary

The remote wrapper is supported in Native and Docker Sandbox Linux Claude tool
sessions. The startup sequence is security-sensitive and ordered:

1. the worker creates a process-local nonce for the exact tool-session ID;
2. the root-owned runtime helper validates immutable wrapper and Skill inputs,
   creates the Native session under its dedicated UID or the Docker Sandbox
   session under the fixed Node UID/GID, and returns that UID over the protected
   helper socket;
3. the worker removes `runtime_uid` from the task result;
4. the Node-owned broker creates a mode `0600` Unix socket below a mode `0700`
   directory and applies `u:UID:--x` to the directory plus `u:UID:rw-` to the
   socket;
5. each connection must present the exact nonce and match that UID through
   Linux `SO_PEERCRED`.

The worker rejects UID 0. Docker startup mounts the verified artifact and broker
paths, refreshes the managed Skill, injects the wrapper-first `PATH`, and keeps
the nonce in the process environment rather than argv or its root-owned trusted
spec. The spec distinguishes account bindings from tool sessions and is the
only source for later runtime resource resolution.

Run the real boundary proof after broker or runtime changes:

```sh
tests/linux_ego_browser_uid_acl_test.sh
```

## Diagnosis

The default broker path is
`/run/agent-remote-node/ego-browser-broker.sock`. On Linux, inspect metadata and
ACLs without reading runtime environments or process command lines:

```sh
namei -l /run/agent-remote-node/ego-browser-broker.sock
getfacl --absolute-names --numeric /run/agent-remote-node
getfacl --absolute-names --numeric /run/agent-remote-node/ego-browser-broker.sock
```

Expected base modes are directory `0700` and socket `0600`. Each authorized
runtime UID has only `--x` on the directory and `rw-` on the socket. A successful
filesystem connection is not authorization: the broker still requires the
exact nonce and `SO_PEERCRED` UID.

For a Docker Sandbox session, also inspect the root-owned state without printing
process environments:

```sh
sudo namei -l /var/lib/agent-remote-runtime/docker-sessions/<session-id>/spec.json
sudo getfacl --absolute-names --numeric \
  /var/lib/agent-remote-runtime/docker-sessions/<session-id>
```

The spec must be a regular `0600` root-owned file with `kind=session`, a
positive non-root UID/GID, and managed tmux/sandbox names. A device-control
session grants that UID traversal on the state parents; no task-provided
resource name is used for attach, forwarding, inspection, or stop.

`ego-browser --doctor` reports bounded compatibility and binding metadata. It
must not print tickets, nonce values, session keys, relay URLs, scripts, page
data, or local browser content.

## Content-free metrics

Node logs use `metric=... value=...` key/value events. A collector must attach
`component=node` from trusted service metadata so identically named Bridge
events are not double-counted.

| Metric | Semantics and finite labels |
| --- | --- |
| `ego_browser_bindings_active` | Gauge after each binding refresh; no dynamic labels. |
| `ego_browser_execute_total` | One event per wrapper request; `status={completed,concurrency_conflict,renewal_required,lease_expired,unavailable,timeout,rejected,unknown_result}`. |
| `ego_browser_execute_duration_seconds` | One duration sample with the same finite status. |
| `ego_browser_bytes_total` | Request/response byte contribution; `direction={request,response}` and the same finite status. |

Inspect only metric lines:

```sh
journalctl -u agent-remote-node --since '-15 min' --no-pager |
  awk '/metric=ego_browser_/ {print}'
```

Never add user, device, session, binding, generation, request, URL, filename,
path, script, page, input, output, or artifact content as a label.

Alert on any `unknown_result`, repeated `renewal_required`/`lease_expired`, an
`unavailable` rate above the normal deploy baseline, or an active binding gauge
that remains nonzero after Server revocation. Alert immediately if startup
reports a root runtime UID, ACL application failure, peer credential failure,
sequence-state ownership failure, or immutable artifact mismatch.

## Containment and recovery

1. Revoke or stop the binding through the Server lifecycle path first so the
   generation and durable outbox are committed.
2. Stop `agent-remote-node` when local broker containment is required. Broker
   shutdown clears nonces, permits, session keys, tickets, and pending requests.
3. Do not repair authorization by widening directory or socket modes, granting
   `other`, adding the runtime to the Node group, or running the wrapper as
   root. Restore owner/mode/ACL state through a verified restart.
4. Treat interrupted calls as `unknown_result` and inspect browser state before
   any new request.
5. Verify immutable wrapper/Skill digests, runtime identity allocation,
   the Linux UID/ACL integration, Server/Redis health, and zero stale outbox
   rows. Restart the Node and require a fresh explicit binding generation.
6. Run `ego-browser --doctor` and a bounded canary. Never restore broker memory,
   an old nonce, ticket, permit, sequence lease, generation, or heredoc.
