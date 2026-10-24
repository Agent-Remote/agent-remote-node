# Exact Claude session attachment

The installed SSH gateway accepts the fixed command
`agent-remote-attach --session <canonical UUID> --expected-device <canonical UUID>`.
It compares the expected device against the trusted authorized_keys forced-command
device before contacting the Server. The SSH key identity comes only from the
forced command. Session, device, key and configured Node identifiers must be
canonical nonzero UUIDs.

The gateway sends those four identities to
`POST /api/v1/node-api/attach/exact/verify`, without redirects or legacy fallback.
The strict version 1 response requires `tool_type=claude`, exact identity echoes,
`tmux_session_name`, `runtime_backend`, nullable `container_id` and
`runtime_resource_id`, and boolean `forward_ssh_agent`. Missing, extra, aliased,
duplicate, mistyped or mismatched fields fail before sudo/runtime dispatch. Errors
exposed by this boundary contain no Server response text or credentials. Runtime
selection uses the verified session ID and backend; the privileged helper still
loads its own trusted runtime spec.

`--session-protocol-info` is an isolated configuration-free mode returning
`{"supported":true,"protocol_version":1}`. Every runtime report probes the
configured installed gateway with that exact flag, a two-second deadline and a
256-byte output limit before advertising `session_attach`. The existing binding
probe and legacy attach routes retain their protocols. A worker upgrade alone
cannot enable exact session attachment with an older installed gateway.

This boundary authorizes connection only. It neither starts nor stops sessions,
and does not change tmux's existing attach/takeover behavior.
